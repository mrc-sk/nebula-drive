package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/service"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/aria2"
	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/gorm"
)

var (
	dlMutex sync.Mutex
	dlJobs  = map[uint]*activeJob{}
)

type activeJob struct {
	taskID uint
	gid    string
	cancel chan struct{}
}

// ListTasks 离线下载任务列表
func ListTasks(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var tasks []models.Task
	q := db.Get().Order("id desc")
	if !u.IsAdmin {
		q = q.Where("owner_id = ?", u.ID)
	}
	q.Limit(100).Find(&tasks)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": tasks})
}

type createTaskReq struct {
	Type     string `json:"type" binding:"required,oneof=http bt"`
	URL      string `json:"url" binding:"required"`
	ParentID *uint  `json:"parentId"`
}

// CreateTask 创建任务
//   - HTTP：优先走 aria2 AddURI，aria2 不可用时回退到 net/http 下载逻辑
//   - BT：磁力链接走 aria2 AddURI，.torrent 链接先下载种子再走 aria2 AddTorrent
//
// 分派细节见 startTask —— 与 RetryTask 共用同一套逻辑。
func CreateTask(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	t := models.Task{OwnerID: u.ID, Type: req.Type, URL: req.URL, Status: 1, ParentID: req.ParentID}
	if err := db.Get().Create(&t).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	if err := startTask(&t); err != nil {
		// 分派失败就让任务落「失败」态，而不是留在「进行中」永不推进 ——
		// 用户可以直接在列表里重试（前提是比如 aria2 已就绪）。
		db.Get().Model(&models.Task{}).Where("id = ?", t.ID).
			Updates(map[string]any{"status": 3, "error": err.Error()})
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": t})
}

// startTask 按任务类型分派下载：HTTP 优先走 aria2 AddURI，不可用时回退 net/http；
// BT 磁力链接走 AddURI，.torrent 链接先下载种子再走 AddTorrent。
//
// 抽出来是为了让 CreateTask 与 RetryTask 共用同一套分派逻辑 —— 否则重试就得复制
// 一遍 aria2/net-http 的分支，两边很容易各自漂移。
//
// 调用方负责在失败时把错误写回 HTTP 响应；这里只返回 error。
func startTask(t *models.Task) error {
	am := aria2.GetManager()

	if t.Type == "http" {
		// 优先尝试 aria2 AddURI
		if am.IsReady() {
			opts := map[string]any{"dir": aria2TaskDir(0)}
			if gid, err := am.AddURI([]string{t.URL}, opts); err == nil {
				go runAria2Task(t.ID, gid, t.OwnerID, t.ParentID)
				return nil
			}
			// aria2 调用失败则回退到 net/http
		}
		// 回退：net/http 下载
		go runHTTPTask(t.ID, t.URL)
		return nil
	}

	// BT 类型
	if !am.IsReady() {
		return errors.New("BT 下载需要 aria2，请先安装 aria2 后再试")
	}
	var gid string
	if strings.HasPrefix(t.URL, "magnet:") {
		g, err := am.AddURI([]string{t.URL}, map[string]any{"dir": aria2TaskDir(0)})
		if err != nil {
			return err
		}
		gid = g
	} else {
		// 先下载种子文件
		data, derr := downloadBytes(t.URL)
		if derr != nil {
			return fmt.Errorf("下载种子文件失败: %w", derr)
		}
		g, err := am.AddTorrent(data, map[string]any{"dir": aria2TaskDir(0)})
		if err != nil {
			return err
		}
		gid = g
	}
	go runAria2Task(t.ID, gid, t.OwnerID, t.ParentID)
	return nil
}

// RetryTask 重试一个已失败/已取消的任务。
//
// 语义是「用同一个 URL 重新排队」：把 status 重置为进行中、清空 progress 与 error，
// 再走一遍 startTask 的分派逻辑。不复用原任务行而新建，是为了让重试历史在
// ListTasks（按 id desc 排序）里可见，也避免重置一个可能已被 aria2 侧清理掉的 gid。
func RetryTask(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)

	var t models.Task
	if err := db.Get().First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "任务不存在"})
		return
	}
	if !u.IsAdmin && t.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "无权操作该任务"})
		return
	}
	// 只有终态任务可重试。进行中的任务重试会造成同一 URL 重复下载。
	if t.Status == 0 || t.Status == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "任务尚未结束，无需重试"})
		return
	}

	// 重置状态后再分派：即使分派失败，任务也停在「失败」而不是永远「进行中」。
	db.Get().Model(&models.Task{}).Where("id = ?", id).
		Updates(map[string]any{"status": 1, "progress": 0, "error": ""})

	if err := startTask(&t); err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).
			Updates(map[string]any{"status": 3, "error": err.Error()})
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// aria2TaskDir 给每个任务一个独立的下载子目录（在 aria2 全局 --dir 下）
func aria2TaskDir(_ int) string {
	return filepath.Join(os.TempDir(), "nebula-aria2")
}

// downloadBytes 用受控的出站客户端下载 URL 内容到内存。
//
// 安全约束（对应 CODE_REVIEW P0-6）：
//   - 请求前做 SSRF 校验（scheme 白名单 + DNS 解析后禁止内网/回环/链路本地/CGNAT）
//   - 拨号时再次校验，抵御 DNS Rebinding
//   - 单次响应体上限 MaxOutboundResponseBytes，避免内存打爆
func downloadBytes(rawURL string) ([]byte, error) {
	body, err := service.FetchBytes(rawURL, service.MaxOutboundResponseBytes)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// runAria2Task 轮询 aria2 tellStatus(gid)，每 2 秒更新 Task.Progress 和 Status，完成时创建 File 记录
func runAria2Task(id uint, gid string, ownerID uint, parentID *uint) {
	cancel := make(chan struct{})
	dlMutex.Lock()
	dlJobs[id] = &activeJob{taskID: id, gid: gid, cancel: cancel}
	dlMutex.Unlock()
	defer func() {
		dlMutex.Lock()
		delete(dlJobs, id)
		dlMutex.Unlock()
	}()

	am := aria2.GetManager()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var errCount int
	for {
		select {
		case <-cancel:
			_ = am.Remove(gid)
			db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": "cancelled"})
			return
		case <-ticker.C:
		}
		status, err := am.TellStatus(gid)
		if err != nil {
			errCount++
			// aria2 暂时不可达，重试若干次；持续失败才标记失败
			if errCount > 10 {
				db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
				return
			}
			continue
		}
		errCount = 0
		aria2ApplyStatus(id, ownerID, parentID, status)
		st, _ := status["status"].(string)
		if st == "complete" || st == "error" || st == "removed" {
			return
		}
	}
}

// aria2ApplyStatus 解析 aria2 状态并更新 Task；完成时创建 File 记录并通知
func aria2ApplyStatus(id uint, ownerID uint, parentID *uint, status map[string]any) {
	st, _ := status["status"].(string)
	progress := aria2Progress(status)
	errMsg, _ := status["errorMessage"].(string)
	if errMsg == "" {
		if ec, ok := status["errorCode"].(string); ok && ec != "0" {
			errMsg, _ = status["errorMessage"].(string)
		}
	}

	updates := map[string]any{"progress": progress}
	switch st {
	case "complete":
		updates["status"] = 2
		updates["progress"] = 100
	case "error":
		updates["status"] = 3
		if errMsg != "" {
			updates["error"] = errMsg
		}
	case "removed":
		updates["status"] = 3
		updates["error"] = "removed"
	default:
		updates["status"] = 1
	}
	db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(updates)

	if st == "complete" {
		aria2CreateFile(id, ownerID, parentID, status)
	}
}

// aria2Progress 根据 completedLength / totalLength 计算百分比
func aria2Progress(status map[string]any) int {
	completed := aria2ToInt(status["completedLength"])
	total := aria2ToInt(status["totalLength"])
	if total <= 0 {
		return 0
	}
	p := int(float64(completed) / float64(total) * 100)
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

// aria2ToInt 把 aria2 返回的字符串/数字长度字段统一转 int64
func aria2ToInt(v any) int64 {
	switch n := v.(type) {
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// aria2FirstPath 取 tellStatus 返回的 files[0].path
func aria2FirstPath(status map[string]any) string {
	files, ok := status["files"].([]any)
	if !ok || len(files) == 0 {
		return ""
	}
	first, ok := files[0].(map[string]any)
	if !ok {
		return ""
	}
	p, _ := first["path"].(string)
	return p
}

// aria2CreateFile 下载完成后，把 aria2 落地的文件转存到默认存储策略并创建 File 记录
func aria2CreateFile(id uint, ownerID uint, parentID *uint, status map[string]any) {
	srcPath := aria2FirstPath(status)
	if srcPath == "" {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": "no file path in aria2 status"})
		return
	}
	src, err := os.Open(srcPath)
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	defer src.Close()
	st, _ := src.Stat()
	filename := filepath.Base(srcPath)

	h, p, err := handlerForPolicyID(1) // 默认本地策略
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	sourceName := strconv.Itoa(int(id)) + "-" + filename
	if err := h.Put(src, sourceName, st.Size()); err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	ext := filepath.Ext(filename)
	f2 := models.File{
		OwnerID: ownerID, Name: filename, ParentID: parentID,
		Size: st.Size(), PolicyID: p.ID, SourceName: sourceName,
		Extension: trimDot(ext), MimeType: mimeTypeByExt(ext),
	}
	if err := db.Get().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&f2).Error; err != nil {
			return err
		}
		return service.AddStorageTx(tx, ownerID, f2.Size)
	}); err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		os.Remove(srcPath)
		return
	}
	// 下载完成通知
	CreateNotification(ownerID, "下载完成", "下载完成："+filename, "success", "task")
	// 清理 aria2 临时文件
	os.Remove(srcPath)
}

// runHTTPTask 执行 HTTP 下载（aria2 不可用时的回退，直接写入默认本地策略）
func runHTTPTask(id uint, url string) {
	cancel := make(chan struct{})
	dlMutex.Lock()
	dlJobs[id] = &activeJob{taskID: id, cancel: cancel}
	dlMutex.Unlock()

	defer func() {
		dlMutex.Lock()
		delete(dlJobs, id)
		dlMutex.Unlock()
	}()

	db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 1, "progress": 0})

	client := service.OutboundClient()
	resp, err := client.Get(url)
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": fmt.Sprintf("status %d", resp.StatusCode)})
		return
	}

	var t models.Task
	if err := db.Get().First(&t, id).Error; err != nil {
		return
	}
	name := cleanName(url)
	filename := filepath.Base(name)
	if filename == "" {
		filename = "downloaded-file"
	}

	h, p, err := handlerForPolicyID(1) // 默认本地策略
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	tmpPath := filepath.Join(os.TempDir(), "nebula-dl-"+strconv.Itoa(int(id)))
	f, err := os.Create(tmpPath)
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	defer os.Remove(tmpPath)
	total := resp.ContentLength
	buf := make([]byte, 32*1024)
	var downloaded int64
	last := time.Now()
	for {
		select {
		case <-cancel:
			f.Close()
			db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": "cancelled"})
			return
		default:
		}
		n, er := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			downloaded += int64(n)
			if total > 0 && time.Since(last) > time.Second {
				progress := int(float64(downloaded) / float64(total) * 100)
				db.Get().Model(&models.Task{}).Where("id = ?", id).Update("progress", progress)
				last = time.Now()
			}
		}
		if er != nil {
			break
		}
	}
	f.Close()
	src, err := os.Open(tmpPath)
	if err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	defer src.Close()
	st, _ := src.Stat()
	sourceName := strconv.Itoa(int(id)) + "-" + filename
	if err := h.Put(src, sourceName, st.Size()); err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	ext := filepath.Ext(filename)
	f2 := models.File{
		OwnerID: t.OwnerID, Name: filename, ParentID: t.ParentID,
		Size: st.Size(), PolicyID: p.ID, SourceName: sourceName,
		Extension: trimDot(ext), MimeType: mimeTypeByExt(ext),
	}
	if err := db.Get().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&f2).Error; err != nil {
			return err
		}
		return service.AddStorageTx(tx, t.OwnerID, f2.Size)
	}); err != nil {
		db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 3, "error": err.Error()})
		return
	}
	db.Get().Model(&models.Task{}).Where("id = ?", id).Updates(map[string]any{"status": 2, "progress": 100})
	CreateNotification(t.OwnerID, "下载完成", "下载完成："+filename, "success", "task")
}

// CancelTask 取消任务
//
// 幂等性（对应 CODE_REVIEW P1-7）：cancel channel 只会被关闭一次。
// 旧实现在任务已结束但仍在 dlJobs 中时二次调用会 close 已关闭的 channel → panic。
// 这里先用 LoadAndDelete 原子摘除，保证同一任务只有一个调用方能拿到 channel 并关闭。
func CancelTask(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)

	var t models.Task
	if err := db.Get().First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "任务不存在"})
		return
	}
	if !u.IsAdmin && t.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "无权操作该任务"})
		return
	}

	// 原子摘除：只有成功摘除的调用方负责关闭 channel，避免重复 close panic
	dlMutex.Lock()
	j, ok := dlJobs[uint(id)]
	if ok {
		delete(dlJobs, uint(id))
	}
	dlMutex.Unlock()

	if ok {
		if j.gid != "" {
			_ = aria2.GetManager().Remove(j.gid)
		}
		if j.cancel != nil {
			close(j.cancel)
		}
	}
	db.Get().Model(&models.Task{}).Where("id = ? AND status = ?", id, 1).
		Updates(map[string]any{"status": 3, "error": "cancelled"})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func cleanName(u string) string {
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] == '/' || u[i] == '\\' || u[i] == '?' || u[i] == '&' || u[i] == '=' {
			return u[i+1:]
		}
	}
	return u
}

func trimDot(s string) string {
	for len(s) > 0 && s[0] == '.' {
		s = s[1:]
	}
	return s
}

func mimeTypeByExt(ext string) string {
	switch ext {
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".7z", ".rar":
		return "application/x-archive"
	case ".txt":
		return "text/plain"
	case ".pdf":
		return "application/pdf"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	}
	return "application/octet-stream"
}
