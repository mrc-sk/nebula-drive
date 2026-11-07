package controllers

import (
	"archive/zip"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// 备份与恢复。
//
// # 设计边界（都是刻意的，别当成没做完）
//
//   - **备份包 = SQLite 库 + 文件清单**。不上传用户文件本体：
//     存储后端可能是 S3/OSS，把文件全拉回来打包既慢又可能放不下。
//     清单里带每个文件的来源名与策略，配合存储后端自身的快照即可还原。
//   - **MySQL / PostgreSQL 不支持自动备份**：拿不到一致的库文件快照，
//     硬做出来的是个坏备份。这种情况在响应里明说，只导出文件清单。
//   - **恢复是暂存制，重启生效**。热替换正在被使用的 SQLite 库
//     （活动连接 + WAL）会直接损坏数据，这不是保守，是唯一安全的做法。
//
// # 为什么文件名要严格校验
//
// 下载接口按名字取 `<dataDir>/backups/<name>`。名字来自查询参数，
// 若直接拼接就能用 `../../etc/passwd` 读任意文件 ——
// 所以既限制字符集，又做一次解析后前缀校验（双保险）。

const (
	// backupManifestMaxFiles 清单里最多列多少个文件。
	// 清单是给人看/给插件用的元数据，不是数据主体；百万级文件全塞进去
	// 只会让备份包变成另一个问题。
	backupManifestMaxFiles = 20000
	// backupUploadMaxBytes 上传备份包的大小上限。
	backupUploadMaxBytes = 4 << 30 // 4GB
	// backupDBEntry 备份包里数据库条目的固定名字。
	backupDBEntry = "nebula.db"
	// backupManifestEntry 清单条目的固定名字。
	backupManifestEntry = "manifest.json"
)

// backupNameRe 备份文件名允许的字符集：由服务端生成，不接受任何路径成分。
var backupNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.zip$`)

// sqliteMagic 是 SQLite 文件头，用来在恢复前确认"这确实是个库文件"。
const sqliteMagic = "SQLite format 3\x00"

func backupDir() string  { return filepath.Join(conf.DataDir(), "backups") }
func restoreDir() string { return filepath.Join(conf.DataDir(), "restore") }

func pendingRestoreMarker() string { return filepath.Join(restoreDir(), "PENDING") }

// liveDBPath 返回当前 SQLite 库文件路径（非 sqlite 返回空）。
//
// 注意这是**相对路径**：conf 里存的就是安装时填的值，相对于进程工作目录解析。
// 这里沿用同样的解析方式，不额外做绝对化 —— 否则会和 db.Init 打开的不是同一个文件。
func liveDBPath() string {
	c := conf.Current()
	if c == nil || c.DB.Type != "sqlite" {
		return ""
	}
	return c.DB.File
}

// backupManifest 备份包里的清单。
type backupManifest struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	DBType    string    `json:"dbType"`
	DBFile    string    `json:"dbFile"`
	DBIncluded bool     `json:"dbIncluded"`
	FileCount int64     `json:"fileCount"`
	Truncated bool      `json:"truncated"`
	Files     []gaugedFile `json:"files"`
}

type gaugedFile struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	ParentID  *uint  `json:"parentId,omitempty"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	PolicyID  uint   `json:"policyId"`
	Source    string `json:"sourceName,omitempty"`
	Deleted   bool   `json:"deleted"`
}

// CreateBackup 生成备份包并触发 onBackup 钩子。
func CreateBackup(c *gin.Context) {
	if db.Get() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "数据库未就绪"})
		return
	}

	man, err := buildBackup(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	if err := os.MkdirAll(backupDir(), 0o750); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "创建备份目录失败: " + err.Error()})
		return
	}
	name := fmt.Sprintf("backup-%d-%s.zip", time.Now().Unix(), randSuffix())
	full := filepath.Join(backupDir(), name)

	f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "写入备份失败: " + err.Error()})
		return
	}
	zw := zip.NewWriter(f)
	writeErr := writeBackupArchive(zw, man)
	if err := zw.Close(); err != nil && writeErr == nil {
		writeErr = err
	}
	_ = f.Close()
	if writeErr != nil {
		_ = os.Remove(full)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "打包失败: " + writeErr.Error()})
		return
	}

	sum, size, err := fileSHA256(full)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	// onBackup 是异步通知钩子：插件可借此把包复制到 S3 / 异地，或落审计。
	// 拿不到返回值是设计使然 —— 备份已经生成，不该因为插件没响应就失败。
	plugin.Fire(plugin.HookBackup, map[string]any{
		"kind":      "full",
		"filename":  name,
		"path":      full,
		"size":      size,
		"sha256":    sum,
		"dbPath":    man.DBFile,
		"dbIncluded": man.DBIncluded,
		"fileCount": man.FileCount,
		"createdAt": man.CreatedAt.Format(time.RFC3339),
		"operator":  operatorName(c),
	})

	auditPluginOp(c, "backup_create", name,
		fmt.Sprintf("size=%d,sha256=%s,files=%d", size, sum, man.FileCount))

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"name":        name,
		"size":        size,
		"sha256":      sum,
		"fileCount":   man.FileCount,
		"dbIncluded":  man.DBIncluded,
		"truncated":   man.Truncated,
		"download":    "/api/admin/backup/download?name=" + name,
		"message":     "备份已生成",
	}})
}

// buildBackup 组装清单（打包动作留给调用方，便于测试单独验证清单）。
func buildBackup(c *gin.Context) (*backupManifest, error) {
	cur := conf.Current()
	man := &backupManifest{
		Version:   "1",
		CreatedAt: time.Now(),
		DBType:    cur.DB.Type,
	}
	if p := liveDBPath(); p != "" {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			man.DBFile = p
			man.DBIncluded = true
		}
	}

	var total int64
	if err := db.Get().Model(&models.File{}).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计文件失败: %w", err)
	}
	man.FileCount = total

	var files []models.File
	if err := db.Get().Model(&models.File{}).Order("id asc").
		Limit(backupManifestMaxFiles).Find(&files).Error; err != nil {
		return nil, fmt.Errorf("读取文件清单失败: %w", err)
	}
	man.Truncated = total > int64(len(files))
	for _, f := range files {
		man.Files = append(man.Files, gaugedFile{
			ID: f.ID, Name: f.Name, ParentID: f.ParentID, IsDir: f.IsDir,
			Size: f.Size, PolicyID: f.PolicyID, Source: f.SourceName,
			Deleted: f.DeletedAt.Valid,
		})
	}
	_ = c
	return man, nil
}

// writeBackupArchive 把清单与（SQLite 时）库文件写进 zip。
func writeBackupArchive(zw *zip.Writer, man *backupManifest) error {
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	w, err := zw.Create(backupManifestEntry)
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}

	if !man.DBIncluded {
		return nil
	}
	src, err := os.Open(man.DBFile)
	if err != nil {
		return err
	}
	defer src.Close()
	dw, err := zw.Create(backupDBEntry)
	if err != nil {
		return err
	}
	_, err = io.Copy(dw, src)
	return err
}

// DownloadBackup 下载已生成的备份包。
func DownloadBackup(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	// 字符集 + 形状校验：不接受任何路径成分
	if !backupNameRe.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "备份名非法"})
		return
	}
	full := filepath.Join(backupDir(), name)

	// 双保险：即使正则被改松，解析后也必须仍在备份目录内
	absDir, err1 := filepath.Abs(backupDir())
	absFull, err2 := filepath.Abs(full)
	if err1 != nil || err2 != nil ||
		(absFull != absDir && !strings.HasPrefix(absFull, absDir+string(filepath.Separator))) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "备份名非法"})
		return
	}
	if st, err := os.Stat(absFull); err != nil || !st.Mode().IsRegular() {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "备份不存在"})
		return
	}
	c.FileAttachment(absFull, name)
}

// RestoreBackup 接收备份包并**暂存**，下次启动时应用。
func RestoreBackup(c *gin.Context) {
	if db.Get() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "数据库未就绪"})
		return
	}
	if liveDBPath() == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "当前数据库不是 SQLite，无法用备份包恢复（请用数据库自身的恢复手段）",
		})
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少上传文件"})
		return
	}
	if fh.Size > backupUploadMaxBytes {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "备份包过大"})
		return
	}

	src, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读取上传文件失败"})
		return
	}
	defer src.Close()

	tmp, err := os.CreateTemp("", "nebula-restore-*.zip")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, io.LimitReader(src, backupUploadMaxBytes)); err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "保存上传文件失败"})
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	st, _ := tmp.Stat()
	zr, err := zip.NewReader(tmp, st.Size())
	if err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "不是合法的 zip 备份包"})
		return
	}

	// 找到库文件条目并校验 SQLite 文件头 —— 不校验就写进数据目录，
	// 等于允许把任意内容伪装成数据库，下次启动直接打不开。
	var dbEntry *zip.File
	for _, f := range zr.File {
		if filepath.Base(f.Name) == backupDBEntry {
			dbEntry = f
			break
		}
	}
	if dbEntry == nil {
		_ = tmp.Close()
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "备份包里没有 " + backupDBEntry + "（若是 MySQL/PG 备份，请用数据库自身手段恢复）",
		})
		return
	}

	rc, err := dbEntry.Open()
	if err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读取备份内容失败"})
		return
	}
	head := make([]byte, len(sqliteMagic))
	n, _ := io.ReadFull(rc, head)
	_ = rc.Close()
	if n != len(sqliteMagic) || string(head) != sqliteMagic {
		_ = tmp.Close()
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "备份包里的数据库文件头不对，拒绝恢复"})
		return
	}

	if err := os.MkdirAll(restoreDir(), 0o750); err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	staged := filepath.Join(restoreDir(), backupDBEntry)
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = tmp.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	rc2, err := dbEntry.Open()
	if err != nil {
		_ = out.Close()
		_ = tmp.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	_, copyErr := io.Copy(out, rc2)
	_ = rc2.Close()
	_ = out.Close()
	_ = tmp.Close()
	if copyErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "写入暂存文件失败"})
		return
	}

	if err := os.WriteFile(pendingRestoreMarker(),
		[]byte(fmt.Sprintf("%s\n%s\n", fh.Filename, time.Now().Format(time.RFC3339))), 0o600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "写入恢复标记失败"})
		return
	}

	// onRestore 在**应用之前**触发：异步通知，插件可落审计或发告警。
	//
	// 它拦不住这次恢复（真正替换发生在下次启动），所以文档里写明了这一点 ——
	// 需要否决请在权限层解决，而不是指望一个异步钩子。
	plugin.Fire(plugin.HookRestore, map[string]any{
		"filename":   fh.Filename,
		"size":       fh.Size,
		"stagedPath": staged,
		"currentDb":  liveDBPath(),
		"operator":   operatorName(c),
	})

	auditPluginOp(c, "backup_restore_staged", fh.Filename,
		fmt.Sprintf("size=%d,staged=%s", fh.Size, staged))

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"staged":  staged,
		"message": "备份包已校验并暂存。真正的替换在下次启动时进行（热替换正在使用的 SQLite 库不安全）",
	}})
}

// ApplyPendingRestore 在启动时应用暂存的恢复。
//
// 必须在 db.Init **之前**调用：那时本进程还没有打开数据库，
// 换文件是安全的；一旦连上再换，活动连接与 WAL 会造成数据损坏。
//
// 返回是否发生了替换，供启动日志使用。
func ApplyPendingRestore() bool {
	staged := filepath.Join(restoreDir(), backupDBEntry)
	if _, err := os.Stat(pendingRestoreMarker()); err != nil {
		return false
	}
	live := liveDBPath()
	if live == "" {
		return false
	}
	if _, err := os.Stat(staged); err != nil {
		// 标记在但文件没了：清理标记，避免每次启动都白试一遍
		_ = os.Remove(pendingRestoreMarker())
		return false
	}

	// 先把现库挪到一边（出问题时还能手工回滚），再就位。
	// 保留 -wal/-shm 一起挪走：只挪主文件会留下不一致的 WAL。
	backupName := fmt.Sprintf("%s.pre-restore-%d", live, time.Now().Unix())
	if _, err := os.Stat(live); err == nil {
		if err := os.Rename(live, backupName); err != nil {
			// 挪不动就**不继续**：硬覆盖会毁掉现库，而恢复失败还来得及重来
			fmt.Printf("[restore] 暂存恢复失败：无法备份现库 %s: %v\n", live, err)
			return false
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(live + suffix); err == nil {
				_ = os.Rename(live+suffix, backupName+suffix)
			}
		}
	}
	if err := os.Rename(staged, live); err != nil {
		fmt.Printf("[restore] 暂存恢复失败：无法就位 %s: %v\n", live, err)
		// 尽力把原库挪回来，让服务至少还能起来
		_ = os.Rename(backupName, live)
		// 标记改名而不是删除：让管理员能看到"这次恢复失败了"，而不是静默消失
		_ = os.Rename(pendingRestoreMarker(), pendingRestoreMarker()+".failed")
		return false
	}
	_ = os.Remove(pendingRestoreMarker())
	fmt.Printf("[restore] 已应用暂存恢复：%s（原库备份为 %s）\n", live, backupName)
	return true
}

// ---- 小工具 ----

// operatorName 取当前操作者用户名（写审计与钩子 ctx 用）。
func operatorName(c *gin.Context) string {
	if u := middleware.CurrentUser(c); u != nil {
		return u.UserName
	}
	return ""
}

func randSuffix() string {
	b := make([]byte, 4)
	if _, err := crand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano()%10000, 10)
	}
	return hex.EncodeToString(b)
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// auditPluginOp 写一条审计日志（沿用项目既有格式）。
func auditPluginOp(c *gin.Context, action, target, detail string) {
	if db.Get() == nil {
		return
	}
	var uid uint
	if u := middleware.CurrentUser(c); u != nil {
		uid = u.ID
	}
	db.Get().Create(&models.AuditLog{
		UserID:   uid,
		UserName: operatorName(c),
		Action:   action,
		Target:   target,
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   detail,
	})
}
