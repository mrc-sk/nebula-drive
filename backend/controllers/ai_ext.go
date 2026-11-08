package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/ai"
	"github.com/nebula-drive/nebula/internal/hooks"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// 本文件是「智能时代」的业务入口：搜索智能推荐、近期常用、自然语言搜文件、
// 摘要与自动标签、AI 助手、代码补全，以及后台 AI 配置与审计。
//
// 每个功能都遵循同一套三档降级：
//   插件钩子接管 → AI 网关（配了模型） → L0 本地算法（永远可用）
// 因此没填 API Key 时功能不会报错，只是「不那么聪明」。

// FileBrief 推荐卡片用到的精简文件信息。
type FileBrief struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Extension    string    `json:"extension"`
	Size         int64     `json:"size"`
	MimeType     string    `json:"mimeType"`
	UpdatedAt    time.Time `json:"updatedAt"`
	AccessCount  int       `json:"accessCount"`
	LastAccessed time.Time `json:"lastAccessed"`
}

func accessRowsToBriefs(u *models.User, rows []ai.AccessRow) []FileBrief {
	if len(rows) == 0 {
		return []FileBrief{}
	}
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.FileID)
	}
	var files []models.File
	_ = db.Get().Where("id IN ? AND owner_id = ? AND deleted_at IS NULL AND is_dir = ?", ids, u.ID, false).
		Find(&files).Error
	byID := map[uint]models.File{}
	for _, f := range files {
		byID[f.ID] = f
	}
	out := make([]FileBrief, 0, len(rows))
	for _, r := range rows {
		f, ok := byID[r.FileID]
		if !ok {
			continue
		}
		out = append(out, FileBrief{
			ID: f.ID, Name: f.Name, Extension: f.Extension, Size: f.Size,
			MimeType: f.MimeType, UpdatedAt: f.UpdatedAt,
			AccessCount: r.Count, LastAccessed: r.LastAccessed,
		})
	}
	return out
}

// SuggestFiles 搜索框下拉：一次给全联想词 / 搜索历史 / 近期常用 / 热门。
// GET /api/files/suggest?q=xx&limit=8
func SuggestFiles(c *gin.Context) {
	u := middleware.CurrentUser(c)
	q := strings.TrimSpace(c.Query("q"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "8"))
	if limit <= 0 || limit > 30 {
		limit = 8
	}

	// onAIRecommend：插件可整体覆盖推荐结果。
	recCtx := map[string]any{"userId": u.ID, "q": q, "limit": limit}
	plugin.FireIf(plugin.HookAIRecommend, recCtx)

	var history []models.SearchHistory
	_ = db.Get().Where("user_id = ?", u.ID).Order("id desc").Limit(30).Find(&history).Error
	historyWords := make([]string, 0, len(history))
	for _, h := range history {
		if h.Keyword != "" {
			historyWords = append(historyWords, h.Keyword)
		}
	}
	// 最近 200 个文件名也参与联想（只取名字，不读内容）。
	var recentFiles []models.File
	_ = db.Get().Where("owner_id = ? AND deleted_at IS NULL AND is_dir = ?", u.ID, false).
		Order("updated_at desc").Limit(200).Find(&recentFiles).Error
	names := make([]string, 0, len(recentFiles))
	for _, f := range recentFiles {
		names = append(names, f.Name)
	}

	suggestions := ai.LocalSuggest(historyWords, names, q, limit)
	if override := hooks.StrSlice(recCtx, "suggestions"); len(override) > 0 {
		suggestions = override
		if len(suggestions) > limit {
			suggestions = suggestions[:limit]
		}
	}

	recentRows := ai.RecentAccess(u.ID, limit)
	hotRows := ai.HotAccess(u.ID, limit)
	if ids := pluginFileIDs(recCtx, "fileIds"); len(ids) > 0 {
		rows := make([]ai.AccessRow, 0, len(ids))
		for i, id := range ids {
			rows = append(rows, ai.AccessRow{FileID: id, Count: len(ids) - i, LastAccessed: time.Now()})
		}
		recentRows = rows
	}

	cfg := ai.LoadConfig()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"suggestions": suggestions,
		"history":     historyWords[:minLen(historyWords, limit)],
		"recent":      accessRowsToBriefs(u, recentRows),
		"hot":         accessRowsToBriefs(u, hotRows),
		"mode":        ai.ModeOf(cfg),
	}})
}

func pluginFileIDs(m map[string]any, key string) []uint {
	if m == nil {
		return nil
	}
	out := []uint{}
	switch v := m[key].(type) {
	case []any:
		for _, x := range v {
			switch n := x.(type) {
			case float64:
				out = append(out, uint(n))
			case uint:
				out = append(out, n)
			case int:
				out = append(out, uint(n))
			}
		}
	case []uint:
		return v
	case []float64:
		for _, n := range v {
			out = append(out, uint(n))
		}
	}
	return out
}

func minLen(s []string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}

// RecentFiles 最近打开的文件。
// GET /api/files/recent?limit=10
func RecentFiles(c *gin.Context) {
	u := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	rows := ai.RecentAccess(u.ID, limit)
	ctx := map[string]any{"userId": u.ID, "q": "recent", "limit": limit}
	plugin.FireIf(plugin.HookAIRecommend, ctx)
	if ids := pluginFileIDs(ctx, "fileIds"); len(ids) > 0 {
		rows = []ai.AccessRow{}
		for i, id := range ids {
			rows = append(rows, ai.AccessRow{FileID: id, Count: len(ids) - i, LastAccessed: time.Now()})
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": accessRowsToBriefs(u, rows)})
}

// HotFiles 最常访问的文件。
// GET /api/files/hot?limit=10
func HotFiles(c *gin.Context) {
	u := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": accessRowsToBriefs(u, ai.HotAccess(u.ID, limit))})
}

// ClearMyAccess 清空本人的访问埋点（隐私）。
// DELETE /api/files/access
func ClearMyAccess(c *gin.Context) {
	u := middleware.CurrentUser(c)
	ai.ClearAccess(u.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// AIStatus 前端用来决定要不要显示 AI 入口与代码空间。
// GET /api/ai/status
func AIStatus(c *gin.Context) {
	cfg := ai.LoadConfig()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"enabled":          cfg.Enabled,
		"available":        cfg.Enabled && (cfg.APIKey != "" || cfg.Provider != ""),
		"mode":             ai.ModeOf(cfg),
		"codeSpaceEnabled": cfg.CodeSpaceEnable,
		"scenes":           cfg.Scenes,
		"autoSummary":      cfg.AutoSummary,
		"autoTag":          cfg.AutoTag,
		"model":            cfg.Model,
	}})
}

// ---- 自然语言搜文件 ----

type aiSearchReq struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// AISearch 把自然语言问句解析成检索条件并直接返回文件列表。
// POST /api/ai/search
func AISearch(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req aiSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请输入要搜索的内容"})
		return
	}
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 20
	}

	// onAISearch：可拦截；可写 filters / answer 完全接管。
	sctx := map[string]any{"userId": u.ID, "query": req.Query, "scope": "files"}
	if errs := plugin.FireIf(plugin.HookAISearch, sctx); len(errs) > 0 {
		if ok, why := plugin.DecisionOf(errs); !ok {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": why})
			return
		}
	}
	if answer := hooks.Str(sctx, "answer", ""); answer != "" {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"answer": answer, "filters": ai.LocalParseQuery(req.Query),
			"list": []models.File{}, "mode": "plugin",
		}})
		return
	}

	filter := ai.LocalParseQuery(req.Query)
	if raw := hooks.Str(sctx, "filters", ""); raw != "" {
		var pf ai.QueryFilter
		if json.Unmarshal([]byte(raw), &pf) == nil {
			filter = pf
		}
	}

	mode := "local"
	answer := ""
	cfg := ai.LoadConfig()
	if cfg.SceneEnabled(ai.SceneSearch) {
		prompt := "你是网盘文件检索助手。把用户的问题转成 JSON 检索条件，只输出 JSON，不要解释。\n" +
			"可用字段：keyword(文件名关键词), ext(扩展名不带点), type(img|video|audio|doc|archive|code), " +
			"minSize/maxSize(字节整数), after(2006-01-02), sort(name|size|updated_at|created_at), order(asc|desc)。\n" +
			"用户问题：" + req.Query
		if r, err := ai.Complete(c.Request.Context(), u.ID, ai.SceneSearch, prompt, 300); err == nil && !r.Fallback {
			if parsed, ok := parseFilterJSON(r.Text); ok {
				if parsed.Keyword != "" || parsed.Ext != "" || parsed.Type != "" || parsed.After != "" || parsed.MinSize > 0 || parsed.MaxSize > 0 {
					filter = mergeFilter(ai.LocalParseQuery(req.Query), parsed)
				}
				mode = r.Provider
			}
		}
	}

	files := queryFilesByFilter(u, filter, req.Limit)
	// 智能排序：访问次数高的在同等相关度下靠前。
	score := ai.AccessScore(u.ID)
	sort.SliceStable(files, func(i, j int) bool {
		return score[files[i].ID] > score[files[j].ID]
	})
	if answer == "" {
		answer = fmt.Sprintf("按「%s」找到 %d 个文件", req.Query, len(files))
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"answer": answer, "filters": filter, "list": files, "mode": mode,
	}})
}

func parseFilterJSON(text string) (ai.QueryFilter, bool) {
	var f ai.QueryFilter
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		return f, false
	}
	return f, true
}

func mergeFilter(base, llm ai.QueryFilter) ai.QueryFilter {
	if llm.Keyword != "" {
		base.Keyword = llm.Keyword
	}
	if llm.Ext != "" {
		base.Ext = llm.Ext
	}
	if llm.Type != "" {
		base.Type = llm.Type
	}
	if llm.MinSize > 0 {
		base.MinSize = llm.MinSize
	}
	if llm.MaxSize > 0 {
		base.MaxSize = llm.MaxSize
	}
	if llm.After != "" {
		base.After = llm.After
	}
	if llm.Sort != "" {
		base.Sort = llm.Sort
	}
	if llm.Order != "" {
		base.Order = llm.Order
	}
	return base
}

// queryFilesByFilter 复刻 Search 的核心条件，供自然语言搜索复用。
func queryFilesByFilter(u *models.User, f ai.QueryFilter, limit int) []models.File {
	q := db.Get().Model(&models.File{}).
		Where("owner_id = ? AND deleted_at IS NULL AND is_dir = ?", u.ID, false)
	if f.Keyword != "" {
		q = q.Where("name LIKE ?", "%"+f.Keyword+"%")
	}
	if f.Ext != "" {
		q = q.Where("extension = ?", strings.ToLower(strings.TrimPrefix(f.Ext, ".")))
	}
	if f.Type != "" {
		pats := typeToMimePatterns(f.Type)
		if len(pats) > 0 {
			ors := make([]string, 0, len(pats))
			args := make([]any, 0, len(pats))
			for _, p := range pats {
				ors = append(ors, "mime_type LIKE ?")
				args = append(args, p)
			}
			q = q.Where(strings.Join(ors, " OR "), args...)
		}
	}
	if f.MinSize > 0 {
		q = q.Where("size >= ?", f.MinSize)
	}
	if f.MaxSize > 0 {
		q = q.Where("size <= ?", f.MaxSize)
	}
	if f.After != "" {
		if t, err := time.Parse("2006-01-02", f.After); err == nil {
			q = q.Where("updated_at >= ?", t)
		}
	}
	sortBy := f.Sort
	allowed := map[string]bool{"name": true, "size": true, "updated_at": true, "created_at": true}
	if !allowed[sortBy] {
		sortBy = "updated_at"
	}
	order := "desc"
	if f.Order == "asc" {
		order = "asc"
	}
	var out []models.File
	_ = q.Order(sortBy + " " + order).Limit(limit).Find(&out).Error
	return out
}

// ---- 摘要与自动标签 ----

// AISummaryFile 生成（或读缓存）文件摘要与标签。
// POST /api/ai/summary/:id
func AISummaryFile(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil || !ownOrAdmin(c, &f) || f.IsDir {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "文件不存在"})
		return
	}
	fileHash := fmt.Sprintf("%d-%d", f.Size, f.UpdatedAt.UnixNano())
	var cached models.AISummary
	if db.Get().Where("file_id = ? AND file_hash = ?", f.ID, fileHash).First(&cached).Error == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": summaryResp(cached, true)})
		return
	}

	text := readTextForAI(&f, 200<<10)
	// onAISummary：插件可写 summary / tags 接管。
	sctx := map[string]any{
		"userId": u.ID, "fileId": f.ID, "fileName": f.Name,
		"mimeType": f.MimeType, "size": f.Size, "textChars": len(text),
	}
	plugin.FireIf(plugin.HookAISummary, sctx)
	summary := hooks.Str(sctx, "summary", "")
	tags := hooks.StrSlice(sctx, "tags")
	provider := "plugin"
	if summary == "" {
		provider = "local"
		summary, tags = ai.LocalSummary(text, 220)
		cfg := ai.LoadConfig()
		if cfg.SceneEnabled(ai.SceneSummary) && text != "" {
			prompt := "请用中文为下面这个文件写一段不超过 150 字的摘要，并在最后一行用「标签：a, b, c」给出 3~5 个标签。\n" +
				"文件名：" + f.Name + "\n内容：\n" + truncateRunes(text, 6000)
			if r, err := ai.Complete(c.Request.Context(), u.ID, ai.SceneSummary, prompt, 400); err == nil && !r.Fallback {
				parsed, parsedTags := splitSummary(r.Text)
				if parsed != "" {
					summary, tags, provider = parsed, parsedTags, r.Provider
				}
			}
		}
	}

	tagsJSON, _ := json.Marshal(tags)
	rec := models.AISummary{
		FileID: f.ID, OwnerID: f.OwnerID, FileHash: fileHash,
		Summary: summary, Tags: string(tagsJSON), Provider: provider,
	}
	db.Get().Where("file_id = ?", f.ID).Delete(&models.AISummary{})
	_ = db.Get().Create(&rec).Error

	cfg := ai.LoadConfig()
	if cfg.AutoTag && len(tags) > 0 {
		autoTagFile(&f, tags)
	}
	ai.RecordAccess(u.ID, f.ID, "open")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": summaryResp(rec, false)})
}

func summaryResp(s models.AISummary, cached bool) gin.H {
	tags := []string{}
	_ = json.Unmarshal([]byte(s.Tags), &tags)
	return gin.H{
		"summary": s.Summary, "tags": tags, "provider": s.Provider,
		"cached": cached, "updatedAt": s.UpdatedAt,
	}
}

func splitSummary(text string) (string, []string) {
	text = strings.TrimSpace(text)
	tags := []string{}
	lines := strings.Split(text, "\n")
	kept := []string{}
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "标签：") || strings.HasPrefix(l, "标签:") || strings.HasPrefix(l, "Tags:") {
			raw := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(l, "标签："), "标签:"), "Tags:")
			for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' || r == '、' || r == ' ' }) {
				t = strings.TrimSpace(t)
				if t != "" && len(tags) < 8 {
					tags = append(tags, t)
				}
			}
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), tags
}

func autoTagFile(f *models.File, tags []string) {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 24 {
			continue
		}
		var cnt int64
		db.Get().Model(&models.FileTag{}).
			Where("file_id = ? AND owner_id = ? AND tag = ?", f.ID, f.OwnerID, tag).Count(&cnt)
		if cnt == 0 {
			_ = db.Get().Create(&models.FileTag{FileID: f.ID, OwnerID: f.OwnerID, Tag: tag}).Error
		}
	}
}

// ---- AI 助手 ----

type aiAskReq struct {
	Question string   `json:"question"`
	History  []string `json:"history"`
}

// AIAsk 侧边栏 AI 助手。上下文只包含文件名清单，不上传文件内容。
// POST /api/ai/ask
func AIAsk(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req aiAskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请输入问题"})
		return
	}

	// onAIAsk：可拦截；可写 answer 直接回答。
	actx := map[string]any{"userId": u.ID, "question": req.Question, "historyChars": len(strings.Join(req.History, ""))}
	if errs := plugin.FireIf(plugin.HookAIAsk, actx); len(errs) > 0 {
		if ok, why := plugin.DecisionOf(errs); !ok {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": why})
			return
		}
	}
	if answer := hooks.Str(actx, "answer", ""); answer != "" {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"answer": answer, "mode": "plugin"}})
		return
	}

	var files []models.File
	_ = db.Get().Where("owner_id = ? AND deleted_at IS NULL", u.ID).
		Order("updated_at desc").Limit(40).Find(&files).Error
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	filter := ai.LocalParseQuery(req.Question)
	refs := queryFilesByFilter(u, filter, 8)

	cfg := ai.LoadConfig()
	if cfg.SceneEnabled(ai.SceneAsk) {
		prompt := "你是 NebulaDrive 网盘助手。用中文简洁回答问题。\n" +
			"用户最近的文件（仅文件名）：" + strings.Join(names, "、") + "\n" +
			"与问题相关的检索结果：" + fileNamesOf(refs) + "\n" +
			"历史对话：" + strings.Join(lastN(req.History, 6), " | ") + "\n" +
			"问题：" + req.Question
		if r, err := ai.Complete(c.Request.Context(), u.ID, ai.SceneAsk, prompt, 800); err == nil && !r.Fallback {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
				"answer": r.Text, "mode": r.Provider, "refs": refs,
			}})
			return
		}
	}
	// L0 兜底：不假装有模型，给出可执行的检索结果与提示。
	answer := fmt.Sprintf("当前是本地模式（未配置模型）。根据你的问题，我在文件里找到 %d 个相关结果："+fileNamesOf(refs)+"。\n可在后台「AI 设置」里配置模型后获得自然语言回答。", len(refs))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"answer": answer, "mode": "local", "refs": refs,
	}})
}

func fileNamesOf(files []models.File) string {
	if len(files) == 0 {
		return "（无）"
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return strings.Join(names, "、")
}

func lastN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---- 后台 AI 配置与审计 ----

// GetAIConfig GET /api/admin/ai/config
func GetAIConfig(c *gin.Context) {
	data := ai.PublicConfig()
	// 列出已注册的 ai Provider，供后台点名选择。
	names := []string{}
	for _, r := range plugin.GlobalRegistry().Providers(plugin.ProviderAI) {
		names = append(names, r.Name)
	}
	data["providers"] = names
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
}

// SaveAIConfig POST /api/admin/ai/config
func SaveAIConfig(c *gin.Context) {
	var cfg ai.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if err := ai.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	u := middleware.CurrentUser(c)
	hooks.Emit(plugin.HookAIConfigChange, map[string]any{
		"operatorId": u.ID, "operatorName": u.UserName,
		"enabled": cfg.Enabled, "provider": cfg.Provider, "model": cfg.Model,
		"changed": "ai_config",
	})
	_ = db.Get().Create(&models.AuditLog{
		UserID: u.ID, UserName: u.UserName, Action: "admin_update_ai_config",
		Target: "ai", IP: c.ClientIP(), UA: c.Request.UserAgent(),
		Detail: fmt.Sprintf("enabled=%v mode=%s model=%s", cfg.Enabled, ai.ModeOf(cfg), cfg.Model),
	}).Error
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ai.PublicConfig()})
}

// TestAIConfig POST /api/admin/ai/test
func TestAIConfig(c *gin.Context) {
	var cfg ai.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	reply, err := ai.Test(c.Request.Context(), cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"reply": reply}})
}

// AIUsage GET /api/admin/ai/usage —— 用量概览。
func AIUsage(c *gin.Context) {
	var rows []models.AIAuditLog
	_ = db.Get().Order("id desc").Limit(5000).Find(&rows).Error
	total, success, tokens, latency := len(rows), 0, 0, int64(0)
	byScene := map[string]int{}
	byProvider := map[string]int{}
	week := time.Now().AddDate(0, 0, -7)
	weekCount := 0
	for _, r := range rows {
		if r.Success {
			success++
		}
		tokens += r.PromptTokens + r.CompletionTokens
		latency += r.LatencyMs
		byScene[r.Scene]++
		byProvider[r.Provider]++
		if r.CreatedAt.After(week) {
			weekCount++
		}
	}
	avg := int64(0)
	if total > 0 {
		avg = latency / int64(total)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"total": total, "success": success, "failed": total - success,
		"tokens": tokens, "avgLatencyMs": avg, "last7Days": weekCount,
		"byScene": byScene, "byProvider": byProvider,
	}})
}

// AILogs GET /api/admin/ai/logs —— 最近日志。
func AILogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var rows []models.AIAuditLog
	_ = db.Get().Order("id desc").Limit(limit).Find(&rows).Error
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": rows})
}
