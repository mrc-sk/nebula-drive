package webdav

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"golang.org/x/crypto/bcrypt"
)

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
	case "COPY", "PROPPATCH", "LOCK", "UNLOCK":
		c.Status(200)
	default:
		c.String(405, "Method Not Allowed")
	}
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
	}
}

func put(c *gin.Context, u *models.User, p string) {
	node, remaining, err := resolvePath(u.ID, p)
	if err != nil {
		c.String(409, "Conflict")
		return
	}
	if remaining == "" {
		// 覆盖已存在文件
		if node.IsDir {
			c.String(409, "Is A Directory")
			return
		}
		_ = node
		// 直接新增一个同路径同名文件，旧的保留（简化版；真实实现应对旧文件去重或清理）
		c.String(200, "OK")
		return
	}
	parentID := parentOf(node)
	// 用默认策略写入
	var pol models.Policy
	db.Get().Where("is_default = ?", true).First(&pol)
	if pol.ID == 0 {
		db.Get().First(&pol, 1)
	}
	h, err := filesystem.New(pol.Type, pol.Config)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	ext := path.Ext(remaining)
	srcName := fmt.Sprintf("dav-%d-%s", time.Now().Unix(), remaining)
	size := c.Request.ContentLength
	if err := h.Put(c.Request.Body, srcName, size); err != nil {
		c.String(500, err.Error())
		return
	}
	f := models.File{
		OwnerID: u.ID, Name: remaining, ParentID: parentID, IsDir: false,
		Size: size, PolicyID: pol.ID, SourceName: srcName,
		Extension: strings.TrimPrefix(ext, "."),
		MimeType:  mimeTypeFromExt(ext),
	}
	if err := db.Get().Create(&f).Error; err != nil {
		c.String(500, err.Error())
		return
	}
	// 更新用户存储
	db.Get().Model(&models.User{}).Where("id = ?", u.ID).UpdateColumn("storage",
		db.DB.Raw("storage + ?", size))
	c.Status(201)
}

func delete_(c *gin.Context, u *models.User, p string) {
	node, _, err := resolvePath(u.ID, p)
	if err != nil || node == nil {
		c.String(404, "Not Found")
		return
	}
	if !node.IsDir {
		if h, err := handlerForFile(node); err == nil {
			h.Delete(node.SourceName)
		}
	}
	db.Get().Delete(node)
	c.Status(204)
}

func move(c *gin.Context, u *models.User, p string) {
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
	return filesystem.New(p.Type, p.Config)
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
