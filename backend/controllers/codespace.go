package controllers

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/ai"
	"github.com/nebula-drive/nebula/internal/hooks"
	"github.com/nebula-drive/nebula/internal/service"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/util"
	"gorm.io/gorm"
)

// 代码空间：后台总开关默认关；只对纯文本/代码文件开放；
// 保存走「新对象 + 版本归档 + 主记录更新」同一事务，避免秒传共享文件被误改，
// 也让编辑历史能在文件版本里回滚。

const maxCodeFileBytes = 2 << 20 // 2 MB，编辑器不适合打开大文件

var textExtensions = map[string]bool{
	"txt": true, "md": true, "markdown": true, "json": true, "jsonc": true,
	"yaml": true, "yml": true, "toml": true, "xml": true, "csv": true,
	"js": true, "jsx": true, "ts": true, "tsx": true, "mjs": true, "cjs": true,
	"go": true, "py": true, "java": true, "c": true, "h": true, "cpp": true,
	"cc": true, "hpp": true, "cs": true, "rs": true, "rb": true, "php": true,
	"sh": true, "bash": true, "zsh": true, "ps1": true, "bat": true,
	"html": true, "htm": true, "css": true, "scss": true, "less": true,
	"sql": true, "ini": true, "conf": true, "env": true, "log": true,
	"vue": true, "svelte": true, "gradle": true, "properties": true,
	"dockerfile": true, "makefile": true, "gitignore": true,
}

var extLanguage = map[string]string{
	"js": "javascript", "jsx": "javascript", "mjs": "javascript", "cjs": "javascript",
	"ts": "typescript", "tsx": "typescript",
	"py": "python", "go": "go", "java": "java", "c": "c", "h": "c",
	"cpp": "cpp", "cc": "cpp", "hpp": "cpp", "cs": "csharp", "rs": "rust",
	"rb": "ruby", "php": "php", "sh": "shell", "bash": "shell", "zsh": "shell",
	"html": "html", "htm": "html", "css": "css", "scss": "css", "less": "css",
	"json": "json", "jsonc": "json", "md": "markdown", "markdown": "markdown",
	"yaml": "yaml", "yml": "yaml", "sql": "sql", "xml": "xml",
	"vue": "html", "svelte": "html", "toml": "ini", "ini": "ini",
}

// DetectLanguage 由文件名推断 CodeMirror 语言。
func DetectLanguage(name string) string {
	base := strings.ToLower(filepath.Base(name))
	if base == "dockerfile" || base == "makefile" {
		return base
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(base)), ".")
	if lang, ok := extLanguage[ext]; ok {
		return lang
	}
	if textExtensions[ext] {
		return "text"
	}
	return "text"
}

func isTextFile(f *models.File) bool {
	if f.IsDir {
		return false
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(f.Name)), ".")
	if textExtensions[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(f.Name))
	if base == "dockerfile" || base == "makefile" || strings.HasPrefix(base, ".env") {
		return true
	}
	return strings.HasPrefix(f.MimeType, "text/") ||
		strings.Contains(f.MimeType, "json") || strings.Contains(f.MimeType, "javascript") ||
		strings.Contains(f.MimeType, "xml") || strings.Contains(f.MimeType, "yaml")
}

// readTextForAI 读取文件内容供摘要/标签使用（有大小上限、非文本返回空）。
func readTextForAI(f *models.File, maxBytes int64) string {
	if !isTextFile(f) || f.Size > maxBytes*4 {
		return ""
	}
	h, err := handlerForFile(f)
	if err != nil {
		return ""
	}
	rc, err := h.Get(f.SourceName)
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxBytes))
	if err != nil || len(b) == 0 {
		return ""
	}
	if !utf8.Valid(b) {
		return string(bytes.ToValidUTF8(b, nil))
	}
	return string(b)
}

func codeSpaceGuard(c *gin.Context) (*models.User, *models.File, bool) {
	if !ai.LoadConfig().CodeSpaceEnable {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "代码空间未开启"})
		return nil, nil, false
	}
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "文件不存在"})
		return nil, nil, false
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return nil, nil, false
	}
	if !isTextFile(&f) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "该文件不是文本/代码文件"})
		return nil, nil, false
	}
	if f.Size > maxCodeFileBytes {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "文件超过 2 MB，不适合在线编辑"})
		return nil, nil, false
	}
	return u, &f, true
}

// GetFileContent GET /api/files/:id/content
func GetFileContent(c *gin.Context) {
	u, f, ok := codeSpaceGuard(c)
	if !ok {
		return
	}
	content := readTextForAI(f, maxCodeFileBytes)
	var version int
	_ = db.Get().Model(&models.FileVersion{}).Where("file_id = ?", f.ID).
		Select("COALESCE(MAX(version), 0)").Scan(&version).Error
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"id": f.ID, "name": f.Name, "content": content,
		"language": DetectLanguage(f.Name), "size": f.Size,
		"updatedAt": f.UpdatedAt, "version": version,
	}})
	ai.RecordAccess(u.ID, f.ID, "edit")
}

type saveContentReq struct {
	Content string `json:"content"`
}

// SaveFileContent PUT /api/files/:id/content
//
// 归档旧版本、写入新对象、更新主记录、调整配额全部在一个事务里；
// 事务失败会把刚落盘的物理对象删掉，不留孤儿文件。
func SaveFileContent(c *gin.Context) {
	u, f, ok := codeSpaceGuard(c)
	if !ok {
		return
	}
	var req saveContentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	data := []byte(req.Content)
	if int64(len(data)) > maxCodeFileBytes {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "内容超过 2 MB 上限"})
		return
	}
	if !utf8.Valid(data) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "内容不是有效的 UTF-8 文本"})
		return
	}

	h, err := handlerForFile(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	sourceName := util.UUID() + filepath.Ext(f.Name)
	if err := h.Put(bytes.NewReader(data), sourceName, int64(len(data))); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	sum := md5.Sum(data)
	newHash := hex.EncodeToString(sum[:])

	oldSize, oldHash, oldSrc, oldPol := f.Size, f.Hash, f.SourceName, f.PolicyID
	delta := int64(len(data)) - oldSize
	if delta > 0 {
		if err := ensureQuota(f.OwnerID, delta); err != nil {
			_ = h.Delete(sourceName)
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}

	var fv models.FileVersion
	err = db.Get().Transaction(func(tx *gorm.DB) error {
		var last models.FileVersion
		if e := tx.Where("file_id = ?", f.ID).Order("version desc").First(&last).Error; e != nil &&
			!errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		fv = models.FileVersion{
			FileID: f.ID, Version: last.Version + 1,
			Size: oldSize, Hash: oldHash, SourceName: oldSrc, PolicyID: oldPol,
		}
		if e := tx.Create(&fv).Error; e != nil {
			return e
		}
		if e := tx.Model(&models.File{}).Where("id = ?", f.ID).Updates(map[string]any{
			"size": len(data), "hash": newHash, "source_name": sourceName,
		}).Error; e != nil {
			return e
		}
		return service.AddStorageTx(tx, f.OwnerID, delta)
	})
	if err != nil {
		_ = h.Delete(sourceName)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	// 内容变了，摘要缓存失效。
	_ = db.Get().Where("file_id = ?", f.ID).Delete(&models.AISummary{}).Error
	hooks.Emit(plugin.HookFileVersionCreate, map[string]any{
		"userId": u.ID, "fileId": f.ID, "fileName": f.Name,
		"version": fv.Version, "size": len(data), "source": "codespace",
	})
	ai.RecordAccess(u.ID, f.ID, "edit")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"size": len(data), "version": fv.Version, "updatedAt": f.UpdatedAt,
	}})
}

// ---- 代码补全 ----

type aiCompleteReq struct {
	FileID    uint   `json:"fileId"`
	FileName  string `json:"fileName"`
	Language  string `json:"language"`
	Prefix    string `json:"prefix"` // 光标前文本（末尾一段即可）
	Suffix    string `json:"suffix"` // 光标后文本
	Doc       string `json:"doc"`    // 完整文档，用于抽取标识符
	MaxTokens int    `json:"maxTokens"`
}

// AIComplete 代码补全：L0 词法补全永远可用，配了模型再叠一层生成式补全。
// POST /api/ai/complete
func AIComplete(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req aiCompleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Language == "" {
		req.Language = DetectLanguage(req.FileName)
	}
	if len(req.Doc) > maxCodeFileBytes {
		req.Doc = req.Doc[:maxCodeFileBytes]
	}
	if len(req.Prefix) > 4096 {
		req.Prefix = req.Prefix[len(req.Prefix)-4096:]
	}
	if len(req.Suffix) > 1024 {
		req.Suffix = req.Suffix[:1024]
	}

	// onAIComplete：插件可直接提供补全。
	pctx := map[string]any{
		"userId": u.ID, "fileId": req.FileID, "fileName": req.FileName,
		"language": req.Language, "prefix": req.Prefix, "suffix": req.Suffix,
		"maxTokens": req.MaxTokens,
	}
	plugin.FireIf(plugin.HookAIComplete, pctx)

	items := ai.LocalCompletion(req.Doc, req.Prefix, req.Language, 12)
	mode := "local"
	text := ""
	if pluginText := hooks.Str(pctx, "completion", ""); pluginText != "" {
		text, mode = pluginText, "plugin"
	} else if cfg := ai.LoadConfig(); cfg.SceneEnabled(ai.SceneComplete) {
		prompt := fmt.Sprintf("你是代码补全引擎。只输出要插入光标处的代码，不要解释、不要 Markdown 代码块。\n语言：%s\n文件：%s\n\n光标前：\n%s\n\n光标后：\n%s",
			req.Language, req.FileName, truncateRunes(req.Prefix, 1200), truncateRunes(req.Suffix, 300))
		if r, err := ai.Complete(c.Request.Context(), u.ID, ai.SceneComplete, prompt, minTokens(req.MaxTokens, 256)); err == nil && !r.Fallback {
			text, mode = strings.Trim(strings.TrimSpace(r.Text), "`"), r.Provider
			if strings.HasPrefix(text, req.Language) {
				text = strings.TrimSpace(strings.TrimPrefix(text, req.Language))
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"items": items, "text": text, "mode": mode,
		"hint": "本地补全来自当前文件标识符与语言关键字；生成式补全需按 Ctrl+Space 显式触发",
	}})
}

func minTokens(v, def int) int {
	if v <= 0 || v > 1024 {
		return def
	}
	return v
}
