package webdav

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/internal/service"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/storage"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// writeDAVQuotaOrServerError 统一 WebDAV 侧的错误响应：
// 配额超限用 507 Insufficient Storage（RFC 4918 定义），其余归 500。
func writeDAVQuotaOrServerError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrQuotaExceeded) {
		c.String(507, "Quota Exceeded")
		return
	}
	c.String(500, err.Error())
}

// WebDAV 虚拟文件系统：
//   - 通过 Basic Auth 认证用户（用户名 + 密码）
//   - 路径映射到 models.File 树形（目录=File.IsDir，普通文件=File.IsDir=false）
//   - 真实读写通过 filesystem.Handler 代理

// ---- WebDAV XML ----

type multistatus struct {
	XMLName   xml.Name `xml:"DAV: multistatus"`
	Responses []response `xml:"response"`
}
type response struct {
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}
type propstat struct {
	Status string `xml:"status"`
	Prop   prop   `xml:"prop"`
}
type prop struct {
	DisplayName  string `xml:"displayname,omitempty"`
	ResourceType *resType `xml:"resourcetype,omitempty"`
	GetContentLength int64 `xml:"getcontentlength,omitempty"`
	GetContentType string `xml:"getcontenttype,omitempty"`
	GetLastModified string `xml:"getlastmodified,omitempty"`
	GetETag string `xml:"getetag,omitempty"`
}
type resType struct {
	Collection *struct{} `xml:"collection,omitempty"`
}

func davTime(t time.Time) string {
	return t.UTC().Format(time.RFC1123Z)
}

func etag(f *models.File) string {
	h := md5.Sum([]byte(fmt.Sprintf("%d-%d-%d-%s", f.ID, f.Size, f.UpdatedAt.Unix(), f.SourceName)))
	return `"` + hex.EncodeToString(h[:]) + `"`
}

// ---- 路径解析 ----

type pathSegment struct {
	file   models.File
	exists bool
}

// resolvePath 把 URL 路径解析到最后一个存在的节点；返回沿路径的文件序列 + 剩余未解析段
// 为了简化，这里做线性遍历：从根 parentId=nil 开始，逐级按目录名查找子节点
func resolvePath(ownerID uint, p string) (*models.File, string, error) {
	p = path.Clean("/" + p)
	if p == "/" {
		return nil, "", nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	var parentID *uint
	var lastDir *models.File
	for i, name := range parts {
		var child models.File
		q := db.Get().Where("owner_id = ? AND name = ? AND deleted_at IS NULL", ownerID, name)
		if parentID == nil {
			q = q.Where("parent_id IS NULL")
		} else {
			q = q.Where("parent_id = ?", *parentID)
		}
		if err := q.First(&child).Error; err != nil {
			// 剩余未解析段
			remaining := strings.Join(parts[i:], "/")
			if lastDir != nil {
				return lastDir, remaining, nil
			}
			return nil, remaining, nil
		}
		if !child.IsDir {
			// 如果不是目录且不是最后一段，则路径无效
			if i != len(parts)-1 {
				return nil, "", fmt.Errorf("invalid path")
			}
			return &child, "", nil
		}
		lastDir = &child
		tmp := child.ID
		parentID = &tmp
	}
	return lastDir, "", nil
}

// listChildren 列目录下所有条目
func listChildren(ownerID uint, dirID *uint) ([]models.File, error) {
	var files []models.File
	q := db.Get().Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	if dirID == nil {
		q = q.Where("parent_id IS NULL")
	} else {
		q = q.Where("parent_id = ?", *dirID)
	}
	if err := q.Order("is_dir desc, name asc").Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

// findFileAt 在某个目录下按 name 找条目
func findFileAt(ownerID uint, parentID *uint, name string) (*models.File, error) {
	var f models.File
	q := db.Get().Where("owner_id = ? AND name = ? AND deleted_at IS NULL", ownerID, name)
	if parentID == nil {
		q = q.Where("parent_id IS NULL")
	} else {
		q = q.Where("parent_id = ?", *parentID)
	}
	err := q.First(&f).Error
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ---- Basic Auth ----

func basicAuth(c *gin.Context) (*models.User, bool) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Basic ") {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic "))
	if err != nil {
		return nil, false
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return nil, false
	}
	var u models.User
	if err := db.Get().Where("user_name = ? AND status = 0", parts[0]).First(&u).Error; err != nil {
		return nil, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(parts[1])) != nil {
		return nil, false
	}
	// 用户组 WebDAV 开关
	var g models.Group
	if err := db.Get().First(&g, u.GroupID).Error; err == nil && !g.WebDAVEnabled {
		return nil, false
	}
	return &u, true
}

// webdavPathPrefix 读配置项 webdav.path，默认 /dav
func webdavPathPrefix() string {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", "webdav.path").First(&s).Error; err == nil && s.Value != "" {
		return s.Value
	}
	return "/dav"
}

var defaultWebDAVMethods = []string{
	"PROPFIND", "GET", "HEAD", "PUT", "MKCOL", "DELETE", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK",
}

// webdavAllowedMethods 读配置项 webdav.methods（JSON 数组），默认 defaultWebDAVMethods
func webdavAllowedMethods() []string {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", "webdav.methods").First(&s).Error; err == nil && s.Value != "" {
		var methods []string
		if err := json.Unmarshal([]byte(s.Value), &methods); err == nil && len(methods) > 0 {
			return methods
		}
	}
	return defaultWebDAVMethods
}

func methodAllowed(method string) bool {
	if method == "OPTIONS" {
		return true
	}
	for _, m := range webdavAllowedMethods() {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// StartLockGC 启动 WebDAV 过期锁清理协程（每分钟一次）。
// 对应 CODE_REVIEW P0-4：LOCK 现在会真实记录锁，必须有对应的回收，否则锁表会无限增长，
// 且过期的锁会一直阻塞该路径的写入。
func StartLockGC() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			davLocksMu.Lock()
			for tok, e := range davLocks {
				if now.After(e.Expires) {
					delete(davLocks, tok)
				}
			}
			davLocksMu.Unlock()
		}
	}()
}

// Handler 返回 WebDAV Gin Handler（不处理路径前缀；上层 r.Any(davPrefix+"/*p", webdavHandler) 即可）
func Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := basicAuth(c)
		if !ok {
			c.Header("WWW-Authenticate", `Basic realm="NebulaDrive WebDAV"`)
			c.String(401, "Unauthorized")
			c.Abort()
			return
		}
		prefix := webdavPathPrefix()
		reqPath := c.Request.URL.Path
		if strings.HasPrefix(reqPath, prefix) {
			reqPath = reqPath[len(prefix):]
		}
		reqPath, _ = url.PathUnescape(reqPath)
		// 归一化：剥离前缀后变成空串表示 DAV 根目录，补成 "/" 才能走 resolvePath 根逻辑
		if reqPath == "" {
			reqPath = "/"
		}
		serveDAV(c, user, reqPath)
	}
}

func serveDAV(c *gin.Context, u *models.User, p string) {
	method := c.Request.Method
	if !methodAllowed(method) {
		c.String(405, "Method Not Allowed")
		return
	}
	switch method {
	case "OPTIONS":
		allowed := append([]string{"OPTIONS"}, webdavAllowedMethods()...)
		c.Header("Allow", strings.Join(allowed, ","))
		c.Header("DAV", "1,2")
		c.Status(200)
	case "PROPFIND":
		propfind(c, u, p)
	case "MKCOL":
		mkcol(c, u, p)
	case "GET", "HEAD":
		getOrHead(c, u, p, method == "HEAD")
	case "PUT":
		put(c, u, p)
	case "DELETE":
		delete_(c, u, p)
	case "MOVE":
		move(c, u, p)
	case "COPY":
		davCopy(c, u, p)
	case "LOCK":
		davLock(c, u, p)
	case "UNLOCK":
		davUnlock(c, u, p)
	case "PROPPATCH":
		// PROPPATCH 改属性：本实现不支持写入任意属性（无自定义属性存储）。
		// 按 RFC 4918 §9.2 返回 207 且每个属性标记 403 Forbidden，而不是伪装的 200。
		davPropPatch(c, u, p)
	default:
		c.String(405, "Method Not Allowed")
	}
}

// ---- LOCK / UNLOCK ----
// 对应 CODE_REVIEW P0-4：原实现在 serveDAV 里直接 c.Status(200) 伪造成功，
// 但从不记录锁 —— 客户端一旦依赖锁做并发保护就会发生静默数据覆盖。
// 这里实现一个进程内的排他锁表，行为对客户端可观察。

type davLockEntry struct {
	Token   string
	Path    string
	OwnerID uint
	Expires time.Time
}

var (
	davLocksMu sync.Mutex
	davLocks   = map[string]*davLockEntry{} // token -> entry
)

// davLockXML 解析 LOCK 请求体（只取 owner 的 href，宽松解析）
type davLockXML struct {
	Owner struct {
		Href string `xml:"href"`
	} `xml:"owner"`
}

func davLock(c *gin.Context, u *models.User, p string) {
	timeout := 30 * time.Minute
	token := "opaquelocktoken:" + uuid.NewString()
	entry := &davLockEntry{Token: token, Path: p, OwnerID: u.ID, Expires: time.Now().Add(timeout)}
	davLocksMu.Lock()
	davLocks[token] = entry
	davLocksMu.Unlock()

	c.Header("Lock-Token", "<"+token+">")
	c.Header("Timeout", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
	c.Header("Content-Type", `application/xml; charset="utf-8"`)
	lockBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<D:prop xmlns:D="DAV:"><D:lockdiscovery><D:activelock>
<D:locktype><D:write/></D:locktype>
<D:lockscope><D:exclusive/></D:lockscope>
<D:depth>infinity</D:depth>
<D:owner><D:href>%d</D:href></D:owner>
<D:timeout>Second-%d</D:timeout>
<D:locktoken><D:href>%s</D:href></D:locktoken>
<D:lockroot><D:href>%s</D:href></D:lockroot>
</D:activelock></D:lockdiscovery></D:prop>`, u.ID, int(timeout.Seconds()), token, xmlEscape(p))
	c.String(200, lockBody)
}

func davUnlock(c *gin.Context, u *models.User, p string) {
	raw := c.GetHeader("Lock-Token")
	token := strings.Trim(strings.TrimSpace(raw), "<>")
	if token == "" {
		c.String(400, "Lock-Token header required")
		return
	}
	davLocksMu.Lock()
	e, ok := davLocks[token]
	if ok && (e.OwnerID == u.ID || u.IsAdmin) {
		delete(davLocks, token)
	} else {
		ok = false
	}
	davLocksMu.Unlock()
	if !ok {
		c.String(409, "Lock token not found")
		return
	}
	c.Status(204)
}

// davCheckLock 供 PUT/DELETE/MOVE 调用：目标路径若被他人持锁则拒绝写入。
func davCheckLock(c *gin.Context, u *models.User, p string) bool {
	davLocksMu.Lock()
	defer davLocksMu.Unlock()
	now := time.Now()
	for tok, e := range davLocks {
		if now.After(e.Expires) {
			delete(davLocks, tok)
			continue
		}
		if e.Path == p && e.OwnerID != u.ID {
			c.String(423, "Locked")
			return false
		}
	}
	return true
}

// ---- COPY ----
// 对应 CODE_REVIEW P0-4：原实现返回 200 但什么都不做。
// 这里实现单文件/单目录复制（拒绝跨用户、拒绝覆盖已存在目标）。
func davCopy(c *gin.Context, u *models.User, p string) {
	src, _, err := resolvePath(u.ID, p)
	if err != nil || src == nil {
		c.String(404, "Not Found")
		return
	}
	if !davCheckLock(c, u, p) {
		return
	}
	dest := c.GetHeader("Destination")
	if dest == "" {
		c.String(400, "Destination header required")
		return
	}
	dest = stripDAVPrefix(dest)
	dest, _ = url.PathUnescape(dest)
	if !strings.HasPrefix(dest, "/") {
		c.String(400, "invalid Destination")
		return
	}
	if dest == p {
		c.String(403, "source and destination are the same")
		return
	}
	name := path.Base(dest)
	if name == "" || name == "." || name == "/" {
		c.String(400, "invalid Destination")
		return
	}
	// 用父目录路径解析父节点，与 move 保持一致的语义
	destParent, _, perr := resolvePath(u.ID, path.Dir(dest))
	if perr != nil {
		c.String(409, "Destination parent not found")
		return
	}
	parentID := parentOf(destParent)
	if existing, _ := findFileAt(u.ID, parentID, name); existing != nil {
		// 不支持 Overwrite: T，按 RFC 4918 §9.8.5 返回 412 Precondition Failed
		c.String(412, "Destination exists")
		return
	}
	if err := davCopyNode(u, src, parentID, name); err != nil {
		c.String(500, err.Error())
		return
	}
	c.Status(201)
}

// davCopyNode 递归复制节点（文件或目录）
func davCopyNode(u *models.User, src *models.File, parentID *uint, name string) error {
	if src.IsDir {
		nf := models.File{OwnerID: u.ID, Name: name, ParentID: parentID, IsDir: true}
		if err := db.Get().Create(&nf).Error; err != nil {
			return err
		}
		var children []models.File
		db.Get().Where("parent_id = ? AND owner_id = ?", src.ID, u.ID).Find(&children)
		for i := range children {
			if err := davCopyNode(u, &children[i], &nf.ID, children[i].Name); err != nil {
				return err
			}
		}
		return nil
	}
	h, err := handlerForFile(src)
	if err != nil {
		return err
	}
	newSrc := fmt.Sprintf("dav-copy-%d-%s", time.Now().UnixNano(), name)
	if err := h.Copy(src.SourceName, newSrc); err != nil {
		return err
	}
	nf := models.File{
		OwnerID: u.ID, Name: name, ParentID: parentID, IsDir: false,
		Size: src.Size, PolicyID: src.PolicyID, SourceName: newSrc,
		Extension: src.Extension, MimeType: src.MimeType, Hash: src.Hash,
	}
	// 文件记录 + 配额占用同事务（对应 CODE_REVIEW P1-4）；
	// 物理副本已落盘，失败时清理它
	if err := db.Get().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&nf).Error; err != nil {
			return err
		}
		return service.AddStorageTx(tx, u.ID, nf.Size)
	}); err != nil {
		_ = h.Delete(newSrc)
		return err
	}
	storage.Retain(nf.PolicyID, newSrc, nf.Size, nf.Hash, "webdav-copy")
	return nil
}

// davPropPatch 明确拒绝属性写入（RFC 4918 §9.2：不可写属性返回 403）
func davPropPatch(c *gin.Context, u *models.User, p string) {
	if _, _, err := resolvePath(u.ID, p); err != nil {
		c.String(404, "Not Found")
		return
	}
	body := `<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:"><D:response><D:href>` + xmlEscape(p) + `</D:href>
<D:propstat><D:status>HTTP/1.1 403 Forbidden</D:status>
<D:responsedescription>property updates are not supported</D:responsedescription>
</D:propstat></D:response></D:multistatus>`
	c.Header("Content-Type", `application/xml; charset="utf-8"`)
	c.String(207, body)
}

// stripDAVPrefix 去掉 Destination 头里的 scheme://host 与 WebDAV 挂载前缀
func stripDAVPrefix(dest string) string {
	if i := strings.Index(dest, "://"); i >= 0 {
		rest := dest[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			dest = rest[j:]
		} else {
			dest = "/"
		}
	}
	prefix := webdavPathPrefix()
	if prefix != "" && prefix != "/dav" {
		dest = strings.TrimPrefix(dest, prefix)
	}
	dest = strings.TrimPrefix(dest, "/dav")
	if dest == "" {
		dest = "/"
	}
	if !strings.HasPrefix(dest, "/") {
		dest = "/" + dest
	}
	return dest
}

// xmlEscape 转义 XML 文本内容
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// ---- 方法实现 ----

func propfind(c *gin.Context, u *models.User, p string) {
	depth := c.GetHeader("Depth")
	if depth == "" {
		depth = "0"
	}
	node, _, err := resolvePath(u.ID, p)
	if err != nil {
		c.String(404, "Not Found")
		return
	}
	var resps []response

	if p == "/" {
		// 根目录
		resps = append(resps, response{
			Href: "/",
			Propstat: propstat{
				Status: "HTTP/1.1 200 OK",
				Prop: prop{
					DisplayName:  "",
					ResourceType: &resType{Collection: &struct{}{}},
					GetLastModified: davTime(time.Now()),
				},
			},
		})
		if depth != "0" {
			children, _ := listChildren(u.ID, nil)
			for _, ch := range children {
				resps = append(resps, respFromFile(ch))
			}
		}
	} else if node == nil {
		c.String(404, "Not Found")
		return
	} else {
		resps = append(resps, respFromFile(*node))
		if depth != "0" && node.IsDir {
			children, _ := listChildren(u.ID, &node.ID)
			for _, ch := range children {
				resps = append(resps, respFromFile(ch))
			}
		}
	}
	ms := multistatus{Responses: resps}
	x, _ := xml.MarshalIndent(ms, "", "  ")
	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.String(207, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+string(x))
}

func respFromFile(f models.File) response {
	r := response{
		Href: hrefOf(f),
		Propstat: propstat{Status: "HTTP/1.1 200 OK"},
	}
	if f.IsDir {
		r.Propstat.Prop.ResourceType = &resType{Collection: &struct{}{}}
	} else {
		r.Propstat.Prop.GetContentLength = f.Size
		r.Propstat.Prop.GetContentType = f.MimeType
		r.Propstat.Prop.GetETag = etag(&f)
	}
	r.Propstat.Prop.DisplayName = f.Name
	r.Propstat.Prop.GetLastModified = davTime(f.UpdatedAt)
	return r
}

func hrefOf(f models.File) string {
	// 简化：直接返回 /name；PROPFIND 结果仅在根和一级子目录准确，
	// 实际客户端只要层级正确即可（Windows 资源管理器/macOS Finder/nautilus 皆兼容）。
	return "/" + url.PathEscape(f.Name)
}

func mkcol(c *gin.Context, u *models.User, p string) {
	node, remaining, err := resolvePath(u.ID, p)
	if err != nil {
		c.String(409, "Conflict")
		return
	}
	if remaining == "" {
		c.String(405, "Already Exists")
		return
	}
	parentID := parentOf(node)
	f := models.File{OwnerID: u.ID, Name: remaining, ParentID: parentID, IsDir: true}
	if err := db.Get().Create(&f).Error; err != nil {
		c.String(403, err.Error())
		return
	}
	c.Status(201)
}

func getOrHead(c *gin.Context, u *models.User, p string, head bool) {
	node, _, err := resolvePath(u.ID, p)
	if err != nil || node == nil || node.IsDir {
		c.String(404, "Not Found")
		return
	}
	h, err := handlerForFile(node)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	rc, err := h.Get(node.SourceName)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	defer rc.Close()
	if node.MimeType != "" {
		c.Header("Content-Type", node.MimeType)
	}
	c.Header("Content-Length", strconv.FormatInt(node.Size, 10))
	c.Header("ETag", etag(node))
	c.Header("Last-Modified", davTime(node.UpdatedAt))
	// Range
	rng := c.GetHeader("Range")
	if rng != "" && strings.HasPrefix(rng, "bytes=") {
		r := strings.TrimPrefix(rng, "bytes=")
		ps := strings.SplitN(r, "-", 2)
		start, _ := strconv.ParseInt(ps[0], 10, 64)
		var end int64 = node.Size - 1
		if ps[1] != "" {
			end, _ = strconv.ParseInt(ps[1], 10, 64)
		}
		if start > end || end >= node.Size || start < 0 {
			c.String(416, "Range Not Satisfiable")
			return
		}
		rc2, _ := h.Get(node.SourceName)
		defer rc2.Close()
		buf := make([]byte, 64*1024)
		io.CopyN(io.Discard, rc2, start)
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, node.Size))
		c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
		c.Status(206)
		if !head {
			written := int64(0)
			target := end - start + 1
			for written < target {
				n, e := rc2.Read(buf)
				if n > 0 {
					to := int64(n)
					if written+to > target {
						to = target - written
					}
					c.Writer.Write(buf[:to])
					written += to
				}
				if e != nil {
					break
				}
			}
		}
		return
	}
	if !head {
		io.Copy(c.Writer, rc)
	} else {
		// HEAD 必须显式写入 200，否则 Gin 在没有任何 Write 调用的情况下可能不刷响应头，导致客户端等待挂起。
		c.Status(http.StatusOK)
	}
}

func put(c *gin.Context, u *models.User, p string) {
	if !davCheckLock(c, u, p) {
		return
	}
	node, remaining, err := resolvePath(u.ID, p)
	if err != nil {
		c.String(409, "Conflict")
		return
	}

	// ---- 覆盖已存在文件（对应 CODE_REVIEW P0-2）----
	// 原实现直接 c.String(200,"OK") 丢弃请求体，导致客户端以为上传成功但数据全丢。
	// 正确做法：读取新内容 → 写新物理对象 → 换 File 记录指向 → 释放旧物理对象。
	if remaining == "" {
		if node == nil || node.IsDir {
			c.String(409, "Conflict")
			return
		}
		body, mimeType, ext, err := readDAVUpload(c, node.Name)
		if err != nil {
			c.String(415, err.Error())
			return
		}
		h, err := handlerForFile(node)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		newSrc := fmt.Sprintf("dav-%d-%s", time.Now().UnixNano(), node.Name)
		if err := h.Put(bytes.NewReader(body), newSrc, int64(len(body))); err != nil {
			c.String(500, err.Error())
			return
		}
		// 主记录更新 + 增量配额占用放进同一事务（对应 CODE_REVIEW P1-4）。
		// 原实现是「先加配额、再改记录」，中间失败要靠手工补偿回滚，容易漏掉某条路径。
		delta := int64(len(body)) - node.Size
		oldPolicyID, oldSrc := node.PolicyID, node.SourceName
		if err := db.Get().Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.File{}).Where("id = ?", node.ID).Updates(map[string]any{
				"source_name": newSrc, "size": int64(len(body)),
				"policy_id": node.PolicyID, "extension": strings.TrimPrefix(ext, "."),
				"mime_type": mimeType, "hash": "",
			}).Error; err != nil {
				return err
			}
			return service.AddStorageTx(tx, u.ID, delta)
		}); err != nil {
			_ = h.Delete(newSrc)
			writeDAVQuotaOrServerError(c, err)
			return
		}
		storage.Retain(node.PolicyID, newSrc, int64(len(body)), "", "webdav-put-overwrite")
		// 旧的物理对象引用计数 -1，归零时自动删除（沿用 refcnt 的 legacy 安全分支）
		if oh, herr := handlerForFilePolicy(oldPolicyID); herr == nil {
			_, _ = storage.ReleaseObject(oldPolicyID, oldSrc, oh)
		}
		c.Status(204)
		return
	}

	parentID := parentOf(node)

	// ---- 统一安全校验链（对应 CODE_REVIEW P0-3）----
	// 原实现完全绕过 HTTP 上传的「扩展名白名单 → 魔术字节 → 配额 → 审计」链条，
	// 使 WebDAV 成为任意文件落地的后门。这里复用 service 中的同一套实现。
	body, mimeType, ext, err := readDAVUpload(c, remaining)
	if err != nil {
		c.String(415, err.Error())
		return
	}
	size := int64(len(body))

	// 用默认策略写入
	var pol models.Policy
	db.Get().Where("is_default = ?", true).First(&pol)
	if pol.ID == 0 {
		db.Get().First(&pol, 1)
	}
	if pol.ID == 0 {
		c.String(500, "no storage policy configured")
		return
	}
	h, err := filesystem.New(pol.Type, pol.Config.String())
	if err != nil {
		c.String(500, err.Error())
		return
	}
	srcName := fmt.Sprintf("dav-%d-%s", time.Now().UnixNano(), remaining)
	if err := h.Put(bytes.NewReader(body), srcName, size); err != nil {
		c.String(500, err.Error())
		return
	}
	f := models.File{
		OwnerID: u.ID, Name: remaining, ParentID: parentID, IsDir: false,
		Size: size, PolicyID: pol.ID, SourceName: srcName,
		Extension: strings.TrimPrefix(ext, "."),
		MimeType:  mimeType,
	}
	// 文件记录 + 配额占用同事务（对应 CODE_REVIEW P1-4）；
	// 物理对象已落盘，失败时清理它
	if err := db.Get().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&f).Error; err != nil {
			return err
		}
		return service.AddStorageTx(tx, u.ID, size)
	}); err != nil {
		_ = h.Delete(srcName)
		writeDAVQuotaOrServerError(c, err)
		return
	}
	// 新物理文件：refs=1
	storage.Retain(pol.ID, srcName, size, "", "webdav-put")
	auditDAVWrite(c, u, "webdav.put", f)
	c.Status(201)
}

// readDAVUpload 读取 WebDAV PUT 请求体并施加与 HTTP 上传一致的安全校验：
// 扩展名白名单 + 可选图片魔术字节校验 + 体积上限。
func readDAVUpload(c *gin.Context, name string) ([]byte, string, string, error) {
	ext := strings.ToLower(path.Ext(name))
	// path.Ext 返回的是带点的形式（".txt"），而 service.IsAllowedExtension 是
	// 对白名单做精确比对，白名单里的条目不带点（"txt,md,png,..."）。
	// 这里少剥一个点，会让所有 WebDAV PUT 都命中 "file type not allowed" ——
	// 表现是：网盘能挂载、能浏览，但一个文件都传不上去。
	// 对齐 controllers/file.go 的做法（那里传的是 trimDot 之后的值）。
	if !service.IsAllowedExtension(strings.TrimPrefix(ext, ".")) {
		return nil, "", "", fmt.Errorf("file type not allowed: %s", ext)
	}
	maxBytes := int64(service.MaxUploadBytes())
	lr := io.LimitReader(c.Request.Body, maxBytes+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", "", fmt.Errorf("file too large (limit %d bytes)", maxBytes)
	}
	if service.MagicCheckEnabled() && service.IsImageExt(ext) {
		if err := service.CheckImageMagicErr(body, ext); err != nil {
			return nil, "", "", err
		}
	}
	mimeType := mimeTypeFromExt(ext)
	return body, mimeType, ext, nil
}

// auditDAVWrite 记录 WebDAV 写入审计（与 HTTP 上传同一张审计表）
func auditDAVWrite(c *gin.Context, u *models.User, action string, f models.File) {
	uctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"channel":  "webdav",
		"action":   action,
		"fileId":   f.ID,
		"fileName": f.Name,
		"size":     f.Size,
	}
	detail := ""
	if b, err := json.Marshal(uctx); err == nil {
		detail = string(b)
	}
	db.Get().Create(&models.AuditLog{
		UserID: u.ID, UserName: u.UserName, Action: action,
		Target: f.Name, IP: c.ClientIP(), UA: c.Request.UserAgent(), Detail: detail,
	})
}

func delete_(c *gin.Context, u *models.User, p string) {
	if !davCheckLock(c, u, p) {
		return
	}
	node, _, err := resolvePath(u.ID, p)
	if err != nil || node == nil {
		c.String(404, "Not Found")
		return
	}
	var freed int64
	if !node.IsDir {
		if h, err := handlerForFile(node); err == nil {
			_, _ = storage.ReleaseObject(node.PolicyID, node.SourceName, h)
		}
		freed = node.Size
	} else {
		freed = dirTotalSize(u.ID, node.ID)
	}
	// 软删除后释放配额（对应 CODE_REVIEW P2-2 同类问题：避免只删记录不还额度）
	db.Get().Delete(node)
	if freed > 0 {
		_ = service.AddStorageTx(db.Get(), u.ID, -freed)
	}
	c.Status(204)
}

// dirTotalSize 递归统计目录下所有文件的字节数
func dirTotalSize(ownerID, dirID uint) int64 {
	var total int64
	var children []models.File
	db.Get().Where("parent_id = ? AND owner_id = ?", dirID, ownerID).Find(&children)
	for i := range children {
		if children[i].IsDir {
			total += dirTotalSize(ownerID, children[i].ID)
		} else {
			total += children[i].Size
		}
	}
	return total
}

func move(c *gin.Context, u *models.User, p string) {
	if !davCheckLock(c, u, p) {
		return
	}
	dest := c.GetHeader("Destination")
	if dest == "" {
		c.String(400, "Missing Destination")
		return
	}
	// 去掉 Destination 的 scheme/host，只取 path
	if u2 := strings.Index(dest, "://"); u2 >= 0 {
		rest := dest[u2+3:]
		if i := strings.Index(rest, "/"); i >= 0 {
			dest = rest[i:]
		}
	}
	// 去掉 dav 前缀
	prefix := webdavPathPrefix()
	if strings.HasPrefix(dest, prefix) {
		dest = dest[len(prefix):]
	}
	dest, _ = url.PathUnescape(dest)
	srcNode, _, err := resolvePath(u.ID, p)
	if err != nil || srcNode == nil {
		c.String(404, "Not Found")
		return
	}
	destDir, destName, err2 := resolvePath(u.ID, dest)
	if err2 != nil {
		c.String(409, "Conflict")
		return
	}
	updates := map[string]any{}
	if destName != "" {
		updates["name"] = destName
	}
	updates["parent_id"] = parentOf(destDir)
	db.Get().Model(srcNode).Updates(updates)
	c.Status(201)
}

func parentOf(node *models.File) *uint {
	if node == nil {
		return nil
	}
	if !node.IsDir {
		return node.ParentID
	}
	id := node.ID
	return &id
}

func handlerForFile(f *models.File) (filesystem.Handler, error) {
	var p models.Policy
	if err := db.Get().First(&p, f.PolicyID).Error; err != nil {
		return nil, err
	}
	return filesystem.New(p.Type, p.Config.String())
}

// handlerForFilePolicy 按 policy id 取 handler（用于释放旧物理对象）
func handlerForFilePolicy(policyID uint) (filesystem.Handler, error) {
	var p models.Policy
	if err := db.Get().First(&p, policyID).Error; err != nil {
		return nil, err
	}
	return filesystem.New(p.Type, p.Config.String())
}

func mimeTypeFromExt(ext string) string {
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
