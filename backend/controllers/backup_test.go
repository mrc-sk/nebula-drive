package controllers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 备份 / 恢复的接线测试。
//
// 覆盖三类容易出事的地方：
//   - 备份包内容是否真的是可用的（清单能解析、库文件被带上）；
//   - 下载接口的文件名是否挡住了路径穿越（名字来自查询参数！）；
//   - 恢复是否 fail closed（不是合法 SQLite 就拒绝），以及**暂存**
//     与启动时应用的逻辑是否真的换掉了库文件。
//
// 关于"恢复为什么是暂存制"：热替换正在被使用的 SQLite 库会因活动连接
// 与 WAL 直接损坏数据。所以恢复只做校验 + 暂存，真正的替换发生在下次启动
// （ApplyPendingRestore，在 db.Init 之前）。

// fakeSQLite 造一个"文件头合法"的最小 SQLite 文件内容。
//
// 恢复接口就是靠这个文件头判断"这确实是个库文件"的，
// 所以测试必须能造出通过与不通过两种输入。
func fakeSQLite(t *testing.T) []byte {
	t.Helper()
	return append([]byte(sqliteMagic), []byte("fake-sqlite-body-for-test")...)
}

func writeTempSQLite(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "live.db")
	if err := os.WriteFile(p, fakeSQLite(t), 0o600); err != nil {
		t.Fatalf("write temp db: %v", err)
	}
	return p
}

// pointConfAtSQLite 让 conf 指向一个临时 SQLite 文件，liveDBPath() 才会生效。
//
// 用 conf.Save 而不是直接改 Current()：多数测试路径下 Current() 是 nil
// （SetupDB 只 Load 不 Save），直接解引用会 panic。
func pointConfAtSQLite(t *testing.T, dbFile string) {
	t.Helper()
	if err := conf.Save(&conf.Config{
		DB:     conf.DBConfig{Type: "sqlite", File: dbFile},
		System: conf.SystemConfig{SiteName: "backup-test", UploadPath: t.TempDir()},
	}); err != nil {
		t.Fatalf("conf.Save: %v", err)
	}
}

func backupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.POST("/backup", CreateBackup)
	r.GET("/backup/download", DownloadBackup)
	r.POST("/backup/restore", RestoreBackup)
	return r
}

func doBackup(t *testing.T) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := backupRouter()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/backup", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("备份失败: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp.Data
}

// TestCreateBackupArchiveIsUsable 备份包必须真的可用：清单能解析、库文件被带上。
func TestCreateBackupArchiveIsUsable(t *testing.T) {
	testutil.SetupDB(t)
	live := writeTempSQLite(t)
	pointConfAtSQLite(t, live)

	// 造两条文件记录，清单里应能看到
	db.Get().Create(&models.File{OwnerID: 1, Name: "a.txt", Extension: "txt", Size: 11, PolicyID: 1})
	db.Get().Create(&models.File{OwnerID: 1, Name: "docs", IsDir: true, PolicyID: 1})

	data := doBackup(t)
	name, _ := data["name"].(string)
	if name == "" {
		t.Fatalf("响应缺少 name: %v", data)
	}
	if !backupNameRe.MatchString(name) {
		t.Fatalf("服务端生成的名字不符合约定: %q", name)
	}
	if inc, _ := data["dbIncluded"].(bool); !inc {
		t.Errorf("SQLite 场景下应带上库文件: %v", data)
	}

	// 打开归档验证内容
	zr, err := zip.OpenReader(filepath.Join(backupDir(), name))
	if err != nil {
		t.Fatalf("打开备份包失败: %v", err)
	}
	defer zr.Close()

	var manBytes []byte
	dbCopied := false
	for _, f := range zr.File {
		switch f.Name {
		case backupManifestEntry:
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("打开清单失败: %v", err)
			}
			manBytes, _ = readAllClose(rc)
		case backupDBEntry:
			dbCopied = true
		}
	}
	if len(manBytes) == 0 {
		t.Fatal("备份包里没有清单")
	}
	if !dbCopied {
		t.Fatal("备份包里没有数据库文件")
	}

	var man backupManifest
	if err := json.Unmarshal(manBytes, &man); err != nil {
		t.Fatalf("清单不是合法 JSON: %v", err)
	}
	if man.FileCount != 2 {
		t.Errorf("清单 fileCount = %d, want 2", man.FileCount)
	}
	if len(man.Files) != 2 {
		t.Errorf("清单 files 条数 = %d, want 2", len(man.Files))
	}
}

// TestCreateBackupFiresHook onBackup 必须被触发，且 ctx 里的信息足够插件
// 去复制文件或落审计。
func TestCreateBackupFiresHook(t *testing.T) {
	testutil.SetupDB(t)
	pointConfAtSQLite(t, writeTempSQLite(t))

	var got map[string]any
	plugin.Register(plugin.HookBackup, func(ctx map[string]any) (map[string]any, error) {
		got = make(map[string]any, len(ctx))
		for k, v := range ctx {
			got[k] = v
		}
		return nil, nil
	})
	defer plugin.UnregisterAll(plugin.HookBackup)

	data := doBackup(t)
	if got == nil {
		t.Fatal("创建备份未触发 onBackup")
	}
	if got["filename"] != data["name"] {
		t.Errorf("钩子里的 filename 与响应不一致: %v vs %v", got["filename"], data["name"])
	}
	for _, k := range []string{"path", "size", "sha256", "fileCount", "operator"} {
		if _, ok := got[k]; !ok {
			t.Errorf("钩子 ctx 缺少 %q —— 插件据此复制文件/落审计，缺了就用不了", k)
		}
	}
	if got["operator"] != "admin" {
		t.Errorf("operator = %v, want admin", got["operator"])
	}
}

// TestDownloadBackupRejectsTraversal 下载接口的名字来自查询参数，
// 必须挡住路径穿越。
func TestDownloadBackupRejectsTraversal(t *testing.T) {
	testutil.SetupDB(t)
	r := backupRouter()

	bad := []string{
		"../../etc/passwd",
		"..%2f..%2fetc%2fpasswd",
		"/etc/passwd",
		"sub/dir.zip",
		"backup.zip/../../x",
		"a.txt",             // 形状不对（必须是 .zip）
		".zip",              // 合法形状但以点开头
		"backup-1-abc.zip/", // 结尾斜杠
	}
	for _, name := range bad {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/backup/download?name="+name, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("非法名字 %q 应 400，实际 %d", name, w.Code)
		}
	}

	// 形状合法但不存在 → 404（与"名字非法"区分开）
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/backup/download?name=backup-1-deadbeef.zip", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("不存在的备份应 404，实际 %d", w.Code)
	}
}

// TestDownloadBackupServesFile 合法名字能把文件取回来。
func TestDownloadBackupServesFile(t *testing.T) {
	testutil.SetupDB(t)
	if err := os.MkdirAll(backupDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	const name = "backup-1-abc12345.zip"
	if err := os.WriteFile(filepath.Join(backupDir(), name), []byte("zipbytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	backupRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/backup/download?name="+name, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("下载应 200，实际 %d", w.Code)
	}
	if w.Body.String() != "zipbytes" {
		t.Errorf("内容不对: %q", w.Body.String())
	}
}

// multipartBody 构造一个带文件的 multipart 请求体。
func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// zipWith 把若干条目打成 zip。
func zipWith(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func doRestore(t *testing.T, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, ctype := multipartBody(t, "file", "backup.zip", archive)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/backup/restore", body)
	req.Header.Set("Content-Type", ctype)
	backupRouter().ServeHTTP(w, req)
	return w
}

// TestRestoreRejectsBadArchives 恢复必须 fail closed：不合法一律拒绝。
func TestRestoreRejectsBadArchives(t *testing.T) {
	testutil.SetupDB(t)
	pointConfAtSQLite(t, writeTempSQLite(t))

	cases := []struct {
		name    string
		archive []byte
		want    int
	}{
		{"不是 zip", []byte("definitely not a zip"), http.StatusBadRequest},
		{"包里没有库文件", zipWith(t, map[string][]byte{"manifest.json": []byte("{}")}), http.StatusBadRequest},
		{"库文件头不对", zipWith(t, map[string][]byte{
			backupDBEntry: []byte("this is not a sqlite file at all"),
		}), http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := doRestore(t, c.archive)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, c.want, w.Body.String())
			}
		})
	}

	// 被拒时不该留下任何暂存痕迹（否则下次启动会应用一个坏库）
	if _, err := os.Stat(pendingRestoreMarker()); err == nil {
		t.Fatal("被拒的恢复不该留下 PENDING 标记")
	}
	if _, err := os.Stat(filepath.Join(restoreDir(), backupDBEntry)); err == nil {
		t.Fatal("被拒的恢复不该留下暂存文件")
	}
}

// TestRestoreStagesValidArchive 合法备份包应被校验并**暂存**（而不是当场替换）。
func TestRestoreStagesValidArchive(t *testing.T) {
	testutil.SetupDB(t)
	live := writeTempSQLite(t)
	pointConfAtSQLite(t, live)

	var got map[string]any
	plugin.Register(plugin.HookRestore, func(ctx map[string]any) (map[string]any, error) {
		got = make(map[string]any, len(ctx))
		for k, v := range ctx {
			got[k] = v
		}
		return nil, nil
	})
	defer plugin.UnregisterAll(plugin.HookRestore)

	archive := zipWith(t, map[string][]byte{backupDBEntry: fakeSQLite(t)})
	w := doRestore(t, archive)
	if w.Code != http.StatusOK {
		t.Fatalf("合法备份包应 200，实际 %d body=%s", w.Code, w.Body.String())
	}

	staged := filepath.Join(restoreDir(), backupDBEntry)
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("应写入暂存文件: %v", err)
	}
	if _, err := os.Stat(pendingRestoreMarker()); err != nil {
		t.Fatalf("应写入 PENDING 标记: %v", err)
	}
	// 暂存制：**当场不能替换现库**
	cur, _ := os.ReadFile(live)
	if !bytes.Equal(cur, fakeSQLite(t)) {
		t.Error("恢复不应在服务运行期间替换现库（热替换会损坏数据）")
	}

	if got == nil {
		t.Fatal("恢复未触发 onRestore")
	}
	for _, k := range []string{"filename", "size", "stagedPath", "currentDb", "operator"} {
		if _, ok := got[k]; !ok {
			t.Errorf("onRestore ctx 缺少 %q", k)
		}
	}
}

// TestApplyPendingRestoreSwapsDB 启动时应用暂存恢复：
// 现库被换掉、原库留一份可回滚、标记被清掉。
func TestApplyPendingRestoreSwapsDB(t *testing.T) {
	testutil.SetupDB(t)
	live := filepath.Join(t.TempDir(), "live.db")
	if err := os.WriteFile(live, []byte("OLD-DB-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointConfAtSQLite(t, live)

	// 没有标记 → 不该做任何事
	if ApplyPendingRestore() {
		t.Fatal("没有 PENDING 时不该发生替换")
	}

	// 造暂存文件 + 标记
	if err := os.MkdirAll(restoreDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restoreDir(), backupDBEntry), []byte("NEW-DB-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingRestoreMarker(), []byte("backup.zip\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !ApplyPendingRestore() {
		t.Fatal("有 PENDING 应发生替换")
	}
	cur, _ := os.ReadFile(live)
	if string(cur) != "NEW-DB-CONTENT" {
		t.Errorf("现库未被替换: %q", cur)
	}
	if _, err := os.Stat(pendingRestoreMarker()); err == nil {
		t.Error("替换成功后应清掉 PENDING 标记")
	}
	// 原库应留一份（出问题能手工回滚）
	matches, _ := filepath.Glob(live + ".pre-restore-*")
	if len(matches) != 1 {
		t.Fatalf("应保留一份原库备份，实际 %d 个", len(matches))
	}
	old, _ := os.ReadFile(matches[0])
	if string(old) != "OLD-DB-CONTENT" {
		t.Errorf("原库备份内容不对: %q", old)
	}
}

// TestApplyPendingRestoreCleansDanglingMarker 标记在但暂存文件没了：
// 清掉标记，避免每次启动都白试一遍。
func TestApplyPendingRestoreCleansDanglingMarker(t *testing.T) {
	testutil.SetupDB(t)
	pointConfAtSQLite(t, writeTempSQLite(t))

	if err := os.MkdirAll(restoreDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingRestoreMarker(), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ApplyPendingRestore() {
		t.Fatal("没有暂存文件时不该替换")
	}
	if _, err := os.Stat(pendingRestoreMarker()); err == nil {
		t.Error("悬空标记应被清理")
	}
}

// readAllClose 读干净并关闭（zip 条目需要显式 Close）。
func readAllClose(rc io.ReadCloser) ([]byte, error) {
	b, err := io.ReadAll(rc)
	_ = rc.Close()
	return b, err
}
