package ai

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// L0 本地算法：不联网、不配 key、零成本，也是模型不可用时的兜底。
// 原则是「确定性 + 可解释」，不做任何假装成 AI 的随机行为。

// CompletionItem 一条补全候选。
type CompletionItem struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"` // identifier / keyword / snippet
	Detail     string `json:"detail"`
	InsertText string `json:"insertText"`
}

var identRE = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)
var extRE = regexp.MustCompile(`(?i)\.([a-z0-9]{1,8})\b`)
var sizeRE = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(kb|mb|gb|b|字节|K|M|G)`)
var daysRE = regexp.MustCompile(`(\d+)\s*(天|日|周|月|年)`)

// languageKeywords 常见语言关键字与内置符号，用于无模型时的补全。
var languageKeywords = map[string][]string{
	"javascript": {"const", "let", "var", "function", "return", "await", "async", "class", "import", "export", "if", "else", "for", "while", "try", "catch", "throw", "new", "this", "console", "Promise", "JSON", "Object", "Array", "String", "Number", "Boolean", "Map", "Set", "null", "undefined", "true", "false"},
	"typescript": {"const", "let", "var", "function", "return", "await", "async", "interface", "type", "class", "import", "export", "enum", "implements", "extends", "public", "private", "readonly", "Promise", "Record", "Partial", "Array", "string", "number", "boolean", "unknown", "any"},
	"python":     {"def", "return", "class", "import", "from", "as", "if", "elif", "else", "for", "while", "try", "except", "finally", "with", "lambda", "yield", "async", "await", "pass", "raise", "None", "True", "False", "self", "print", "len", "range", "dict", "list", "str", "int"},
	"go":         {"func", "return", "var", "const", "type", "struct", "interface", "package", "import", "if", "else", "for", "range", "switch", "case", "default", "go", "defer", "chan", "map", "make", "new", "nil", "true", "false", "error", "string", "int", "context", "fmt"},
	"java":       {"public", "private", "protected", "class", "interface", "extends", "implements", "static", "final", "void", "return", "new", "if", "else", "for", "while", "try", "catch", "throw", "import", "package", "this", "super", "null", "true", "false"},
	"json":       {"true", "false", "null"},
	"html":       {"div", "span", "class", "id", "href", "src", "script", "style", "head", "body", "meta", "link", "input", "button", "form", "table", "tr", "td"},
	"css":        {"display", "position", "flex", "grid", "color", "background", "border", "margin", "padding", "font-size", "width", "height", "var", "calc", "important"},
	"markdown":   {"#", "##", "###", "-", ">", "```", "**", "[", "]", "(", ")", "!["},
	"sql":        {"SELECT", "FROM", "WHERE", "INSERT", "UPDATE", "DELETE", "JOIN", "ON", "GROUP", "BY", "ORDER", "LIMIT", "AS", "AND", "OR", "NULL", "COUNT", "SUM", "CREATE", "TABLE"},
	"php":        {"<?php", "echo", "function", "return", "class", "public", "private", "protected", "if", "else", "foreach", "while", "try", "catch", "new", "null", "true", "false", "$_GET", "$_POST", "array"},
}

// KeywordsFor 返回某语言的关键字（未知语言返回通用集合）。
func KeywordsFor(language string) []string {
	if v, ok := languageKeywords[strings.ToLower(language)]; ok {
		return v
	}
	return languageKeywords["javascript"]
}

// LocalCompletion 从当前文档抽取标识符 + 语言关键字做补全。
// prefix 是光标前最后一个词，doc 是整个文档内容。
func LocalCompletion(doc, prefix, language string, limit int) []CompletionItem {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	prefix = strings.TrimSpace(prefix)
	// 只取光标前的最后一段标识符作为待补全前缀。
	if i := strings.LastIndexAny(prefix, " \t\n\r(){}[];,.:=+-*/<>\"'"); i >= 0 {
		prefix = prefix[i+1:]
	}

	seen := map[string]bool{}
	items := []CompletionItem{}
	add := func(label, kind, detail string) {
		if label == "" || seen[label] || len(items) >= limit*3 {
			return
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(label), strings.ToLower(prefix)) {
			return
		}
		if label == prefix {
			return
		}
		seen[label] = true
		items = append(items, CompletionItem{
			Label: label, Kind: kind, Detail: detail, InsertText: label,
		})
	}

	// 文档内出现过的标识符优先（更贴合当前文件）。
	for _, m := range identRE.FindAllString(doc, -1) {
		if len(m) < 2 {
			continue
		}
		add(m, "identifier", "当前文件")
	}
	for _, kw := range KeywordsFor(language) {
		add(kw, "keyword", language+" 关键字")
	}

	// 前缀匹配排在前面，其次按长度、字典序，保证结果稳定。
	sort.SliceStable(items, func(i, j int) bool {
		pi := strings.HasPrefix(strings.ToLower(items[i].Label), strings.ToLower(prefix))
		pj := strings.HasPrefix(strings.ToLower(items[j].Label), strings.ToLower(prefix))
		if pi != pj {
			return pi
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "identifier"
		}
		if len(items[i].Label) != len(items[j].Label) {
			return len(items[i].Label) < len(items[j].Label)
		}
		return items[i].Label < items[j].Label
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// LocalSuggest 搜索联想：历史搜索词 + 文件名，按前缀命中、频次、长度加权。
func LocalSuggest(history, fileNames []string, q string, limit int) []string {
	if limit <= 0 || limit > 30 {
		limit = 8
	}
	q = strings.ToLower(strings.TrimSpace(q))
	score := map[string]int{}
	for i, h := range history {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		score[h] += 100 - minInt(i, 50) // 越新权重越高
	}
	for _, n := range fileNames {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		score[n] += 10
	}
	type kv struct {
		k string
		v int
	}
	list := []kv{}
	for k, v := range score {
		l := strings.ToLower(k)
		if q == "" {
			list = append(list, kv{k, v})
			continue
		}
		switch {
		case strings.HasPrefix(l, q):
			list = append(list, kv{k, v + 200})
		case strings.Contains(l, q):
			list = append(list, kv{k, v + 50})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		if len(list[i].k) != len(list[j].k) {
			return len(list[i].k) < len(list[j].k)
		}
		return list[i].k < list[j].k
	})
	out := []string{}
	for _, x := range list {
		out = append(out, x.k)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// LocalSummary 提取式摘要 + 高频词标签（不调模型）。
func LocalSummary(text string, maxRunes int) (string, []string) {
	if maxRunes <= 0 || maxRunes > 2000 {
		maxRunes = 220
	}
	text = strings.TrimSpace(text)
	// 取前若干句作为摘要。
	parts := regexp.MustCompile(`(?m)[。！？.!?\n]+`).Split(text, -1)
	summary := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(summary+p) > maxRunes {
			break
		}
		summary += p + "。"
	}
	if summary == "" && text != "" {
		r := []rune(text)
		if len(r) > maxRunes {
			r = r[:maxRunes]
		}
		summary = string(r) + "…"
	}
	return strings.TrimSuffix(summary, "。"), LocalTags(text, 5)
}

// LocalTags 从文本抽取高频词做标签（中文按 2-gram，英文按单词）。
func LocalTags(text string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "this": true, "that": true,
		"的": true, "了": true, "是": true, "在": true, "和": true, "与": true, "及": true,
		"一个": true, "我们": true, "可以": true, "这个": true, "进行": true,
	}
	freq := map[string]int{}
	for _, w := range identRE.FindAllString(strings.ToLower(text), -1) {
		if len(w) < 3 || stop[w] {
			continue
		}
		freq[w]++
	}
	runes := []rune(text)
	for i := 0; i+1 < len(runes); i++ {
		if !unicode.Is(unicode.Han, runes[i]) || !unicode.Is(unicode.Han, runes[i+1]) {
			continue
		}
		w := string(runes[i : i+2])
		if stop[w] {
			continue
		}
		freq[w]++
	}
	type kv struct {
		k string
		v int
	}
	list := []kv{}
	for k, v := range freq {
		if v < 2 {
			continue
		}
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	out := []string{}
	for _, x := range list {
		out = append(out, x.k)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// QueryFilter 自然语言解析出的检索条件。
type QueryFilter struct {
	Keyword string   `json:"keyword"`
	Ext     string   `json:"ext"`
	Type    string   `json:"type"`
	MinSize int64    `json:"minSize"`
	MaxSize int64    `json:"maxSize"`
	After   string   `json:"after"` // 2006-01-02
	Owner   string   `json:"owner"`
	Sort    string   `json:"sort"`
	Order   string   `json:"order"`
	Terms   []string `json:"terms"`
}

var typeWords = map[string]string{
	"图片": "img", "照片": "img", "image": "img", "images": "img", "picture": "img",
	"视频": "video", "影片": "video", "movie": "video",
	"音频": "audio", "音乐": "audio", "music": "audio",
	"文档": "doc", "文档文件": "doc", "document": "doc", "documents": "doc",
	"压缩包": "archive", "压缩": "archive", "zip": "archive", "archive": "archive",
	"代码": "code", "源码": "code", "code": "code",
	"pdf": "pdf", "word": "docx", "excel": "xlsx", "ppt": "pptx",
}

var extWords = map[string]string{
	"go": "go", "py": "py", "python": "py", "js": "js", "javascript": "js",
	"ts": "ts", "typescript": "ts", "json": "json", "md": "md", "markdown": "md",
	"html": "html", "css": "css", "java": "java", "php": "php", "sql": "sql",
	"txt": "txt", "yaml": "yaml", "yml": "yml", "csv": "csv", "xml": "xml",
	"pdf": "pdf", "docx": "docx", "xlsx": "xlsx", "pptx": "pptx", "zip": "zip",
	"jpg": "jpg", "jpeg": "jpeg", "png": "png", "gif": "gif", "mp4": "mp4",
}

// LocalParseQuery 把自然语言问句解析成后端 Search 能吃的条件。
// 这是 L0 兜底；配置了模型时由 AISearch 用 LLM 解析并允许覆盖。
func LocalParseQuery(q string) QueryFilter {
	f := QueryFilter{Owner: "me", Sort: "updated_at", Order: "desc"}
	raw := strings.TrimSpace(q)
	if raw == "" {
		return f
	}
	lower := strings.ToLower(raw)

	// 扩展名 / 类型
	for word, ext := range extWords {
		if strings.Contains(lower, word) {
			f.Ext = ext
			break
		}
	}
	for word, typ := range typeWords {
		if strings.Contains(lower, word) {
			f.Type = typ
			break
		}
	}
	if m := extRE.FindStringSubmatch(lower); m != nil {
		f.Ext = m[1]
	}

	// 大小：大于/小于/超过/不到
	if m := sizeRE.FindStringSubmatch(lower); m != nil {
		n := parseSize(m[1], m[2])
		idx := strings.Index(lower, m[0])
		before := ""
		if idx > 4 {
			before = lower[idx-4 : idx]
		}
		if strings.Contains(before, "小于") || strings.Contains(before, "不到") || strings.Contains(before, "以下") || strings.Contains(before, "less") {
			f.MaxSize = n
		} else {
			f.MinSize = n
		}
	}

	// 时间：最近 N 天/周/月、今天、本周、本月
	if strings.Contains(lower, "今天") || strings.Contains(lower, "today") {
		f.After = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	} else if strings.Contains(lower, "本周") || strings.Contains(lower, "this week") {
		f.After = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	} else if strings.Contains(lower, "本月") || strings.Contains(lower, "this month") {
		f.After = time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	} else if m := daysRE.FindStringSubmatch(lower); m != nil {
		n := atoiSafe(m[1])
		switch m[2] {
		case "周":
			n *= 7
		case "月":
			n *= 30
		case "年":
			n *= 365
		}
		if n > 0 && n < 3650 {
			f.After = time.Now().AddDate(0, 0, -n).Format("2006-01-02")
		}
	}

	// 排序
	switch {
	case strings.Contains(lower, "最大") || strings.Contains(lower, "largest"):
		f.Sort, f.Order = "size", "desc"
	case strings.Contains(lower, "最新") || strings.Contains(lower, "newest") || strings.Contains(lower, "最近修改"):
		f.Sort, f.Order = "updated_at", "desc"
	case strings.Contains(lower, "最早") || strings.Contains(lower, "oldest"):
		f.Sort, f.Order = "created_at", "asc"
	case strings.Contains(lower, "名字") || strings.Contains(lower, "名称") || strings.Contains(lower, "name"):
		f.Sort, f.Order = "name", "asc"
	}

	// 去掉明确的查询套话，剩下的当关键词。
	cleaner := regexp.MustCompile(`(?i)(帮我|请|找一下|找出|搜索|搜一下|查一下|查询|列出|显示|所有|的|文件|里|中|哪些|给我|一下|about|find|search|show|list|all|my|files|please|me|the|a|an|in|with|larger|than|smaller|recent|last|days|week|month)`)
	cleaned := cleaner.ReplaceAllString(raw, " ")
	for _, term := range strings.FieldsFunc(cleaned, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) {
		t := strings.TrimSpace(term)
		if t == "" || utf8.RuneCountInString(t) < 2 {
			continue
		}
		if _, isType := typeWords[strings.ToLower(t)]; isType {
			continue
		}
		if _, isExt := extWords[strings.ToLower(t)]; isExt {
			continue
		}
		f.Terms = append(f.Terms, t)
	}
	if len(f.Terms) > 0 {
		f.Keyword = f.Terms[0]
	}
	return f
}

func parseSize(num, unit string) int64 {
	var n float64
	for _, r := range num {
		if r >= '0' && r <= '9' {
			n = n*10 + float64(r-'0')
		}
	}
	switch strings.ToLower(unit) {
	case "kb", "k":
		return int64(n * 1024)
	case "mb", "m":
		return int64(n * 1024 * 1024)
	case "gb", "g":
		return int64(n * 1024 * 1024 * 1024)
	default:
		return int64(n)
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
