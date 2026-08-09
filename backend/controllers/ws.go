package controllers

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// wsHub 维护 userID → []*websocket.Conn 的映射（带互斥锁）
type wsHub struct {
	mu       sync.Mutex
	conns    map[uint][]*websocket.Conn
	upgrader websocket.Upgrader
}

var hub = &wsHub{
	conns: map[uint][]*websocket.Conn{},
	upgrader: websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	},
}

func (h *wsHub) register(userID uint, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[userID] = append(h.conns[userID], conn)
}

func (h *wsHub) unregister(userID uint, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.conns[userID]
	for i, c := range conns {
		if c == conn {
			h.conns[userID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(h.conns[userID]) == 0 {
		delete(h.conns, userID)
	}
}

// send 向某用户所有连接推送 JSON 消息
func (h *wsHub) send(userID uint, msg any) {
	h.mu.Lock()
	conns := append([]*websocket.Conn(nil), h.conns[userID]...)
	h.mu.Unlock()
	for _, c := range conns {
		if err := c.WriteJSON(msg); err != nil {
			h.unregister(userID, c)
			_ = c.Close()
		}
	}
}

// WSHandler WebSocket 升级处理（需登录）
func WSHandler(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if u == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	conn, err := hub.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	hub.register(u.ID, conn)
	defer func() {
		hub.unregister(u.ID, conn)
		_ = conn.Close()
	}()
	// 持续读取（丢弃客户端消息，仅用于感知断开）
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// NotifyUser 向用户推送通知（WebSocket）
func NotifyUser(userID uint, n models.Notification) {
	hub.send(userID, gin.H{"type": "notification", "data": n})
}

// CreateNotification 创建通知并推送给用户，同时触发邮件插件钩子
func CreateNotification(userID uint, title, body, ntype, category string) {
	if db.Get() == nil {
		return
	}
	if ntype == "" {
		ntype = "info"
	}
	if category == "" {
		category = "system"
	}
	n := models.Notification{
		UserID:   userID,
		Title:    title,
		Body:     body,
		Type:     ntype,
		Category: category,
	}
	if err := db.Get().Create(&n).Error; err != nil {
		return
	}
	NotifyUser(userID, n)

	// 触发邮件钩子
	var u models.User
	if err := db.Get().First(&u, userID).Error; err == nil && u.Email != "" {
		plugin.Fire(plugin.HookEmail, map[string]any{
			"to":      u.Email,
			"subject": title,
			"body":    body,
		})
	}
}
