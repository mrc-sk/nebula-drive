package controllers

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/storage"
	"github.com/nebula-drive/nebula/pkg/util"
)

// CollabOpen 打开协作编辑：
//   - 触发 onCollabOpen 钩子，若钩子在 ctx 中写入 url 则 302 跳转（OnlyOffice 等）
//   - 否则文件为 md/txt 时返回 {mode:"yjs", docId}
func CollabOpen(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) || f.IsDir {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	ctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"fileId":   f.ID,
		"fileName": f.Name,
		"mimeType": f.MimeType,
		"ip":       c.ClientIP(),
	}
	plugin.Fire(plugin.HookCollabOpen, ctx)
	if url, ok := ctx["url"].(string); ok && url != "" {
		c.Redirect(http.StatusFound, url)
		return
	}
	// 没有钩子且文件是 md/txt → 返回 yjs 模式
	if isTextLike(&f) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"mode":  "yjs",
			"docId": collabDocID(f.ID),
		}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 1, "message": "no collab handler"})
}

func isTextLike(f *models.File) bool {
	switch strings.ToLower(f.Extension) {
	case "md", "txt", "markdown":
		return true
	}
	switch f.MimeType {
	case "text/plain", "text/markdown":
		return true
	}
	return false
}

func collabDocID(fileID uint) string {
	return strconv.FormatUint(uint64(fileID), 10)
}

type collabSaveReq struct {
	Content string `json:"content"`
}

// CollabSave 保存协作内容：触发 onCollabSave 钩子，同时把当前版本归档并写入新内容
func CollabSave(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var req collabSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 协作保存大小差过配额
	delta := int64(len(req.Content)) - f.Size
	if delta > 0 {
		if err := ensureQuota(f.OwnerID, delta); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}

	plugin.Fire(plugin.HookCollabSave, map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"fileId":   f.ID,
		"fileName": f.Name,
		"content":  req.Content,
	})

	content := []byte(req.Content)
	hw := md5.New()
	hw.Write(content)
	newHash := hex.EncodeToString(hw.Sum(nil))

	h, err := handlerForFile(&f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	// 归档当前版本
	var last models.FileVersion
	db.Get().Where("file_id = ?", f.ID).Order("version desc").First(&last)
	hv := models.FileVersion{
		FileID: f.ID, Version: last.Version + 1,
		Size: f.Size, Hash: f.Hash, SourceName: f.SourceName, PolicyID: f.PolicyID,
	}
	if err := db.Get().Create(&hv).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	newSource := util.UUID() + filepath.Ext(f.SourceName)
	if err := h.Put(strings.NewReader(req.Content), newSource, int64(len(content))); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	oldSize := f.Size
	f.Size = int64(len(content))
	f.Hash = newHash
	f.SourceName = newSource
	db.Get().Model(&f).Updates(map[string]any{
		"size": f.Size, "hash": newHash, "source_name": newSource,
	})
	addStorage(f.OwnerID, f.Size-oldSize)
	// 协作新内容写新物理文件：refs=1
	storage.Retain(f.PolicyID, newSource, f.Size, newHash, "collab-save-new")

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// ---- Yjs CRDT WebSocket 同步 ----

// collabWSHub 维护 docId → []*websocket.Conn 映射
type collabWSHub struct {
	mu       sync.Mutex
	conns    map[string][]*websocket.Conn
	upgrader websocket.Upgrader
}

var collabHub = &collabWSHub{
	conns: map[string][]*websocket.Conn{},
	upgrader: websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	},
}

func (h *collabWSHub) add(docID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[docID] = append(h.conns[docID], conn)
}

func (h *collabWSHub) remove(docID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.conns[docID]
	for i, c := range conns {
		if c == conn {
			h.conns[docID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(h.conns[docID]) == 0 {
		delete(h.conns, docID)
	}
}

// broadcast 把消息广播给同 docId 的其他连接
func (h *collabWSHub) broadcast(docID string, sender *websocket.Conn, msgType int, msg []byte) {
	h.mu.Lock()
	conns := append([]*websocket.Conn(nil), h.conns[docID]...)
	h.mu.Unlock()
	for _, c := range conns {
		if c == sender {
			continue
		}
		if err := c.WriteMessage(msgType, msg); err != nil {
			h.remove(docID, c)
			_ = c.Close()
		}
	}
}

// CollabWS Yjs CRDT 同步：接收 update 消息广播给同 docId 的其他连接。
// 加严校验：docId 是 fileId（见 collabDocID），当前用户必须是文件 owner / 管理员 / 或该文件存在有效公开分享（不限权限），
// 否则返回 403，避免任意登录用户遍历文件 ID 窃听/篡改其他用户的私有文档。
func CollabWS(c *gin.Context) {
	docID := c.Param("docId")
	if docID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "docId required"})
		return
	}
	// docId → fileId（与 collabDocID 对称）
	fileIDNum, pErr := strconv.ParseUint(docID, 10, 64)
	if pErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid docId"})
		return
	}
	fileID := uint(fileIDNum)

	u := middleware.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}

	// 归属 & 授权校验
	var f models.File
	if err := db.Get().Where("id = ?", fileID).First(&f).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "document not found"})
		return
	}
	isOwnerOrAdmin := u.IsAdmin || f.OwnerID == u.ID
	// 若不是 owner/admin，也允许存在未过期、无密码、无提取码的公开分享让访客协作
	canViaShare := false
	if !isOwnerOrAdmin {
		var share models.Share
		q := db.Get().Where("file_id = ?", f.ID)
		if e := q.First(&share).Error; e == nil {
			notExpired := share.ExpireAt == nil || share.ExpireAt.After(time.Now())
			noGate := share.Password == "" && share.ExtractCode == ""
			if notExpired && noGate {
				canViaShare = true
			}
		}
	}
	if !isOwnerOrAdmin && !canViaShare {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return
	}

	conn, err := collabHub.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	collabHub.add(docID, conn)
	defer func() {
		collabHub.remove(docID, conn)
		_ = conn.Close()
	}()
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage || mt == websocket.TextMessage {
			collabHub.broadcast(docID, conn, mt, msg)
		}
	}
}
