package controllers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	tlspkg "github.com/nebula-drive/nebula/pkg/tls"
)

// UploadCert 上传手动 TLS 证书（multipart：cert.pem + key.pem），存到 data/ssl/
func UploadCert(c *gin.Context) {
	certFile, err := c.FormFile("cert")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "未接收到证书文件"})
		return
	}
	keyFile, err := c.FormFile("key")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "未接收到私钥文件"})
		return
	}
	sslDir := tlspkg.SSLDir()
	if err := os.MkdirAll(sslDir, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	if err := c.SaveUploadedFile(certFile, filepath.Join(sslDir, "cert.pem")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	if err := c.SaveUploadedFile(keyFile, filepath.Join(sslDir, "key.pem")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 校验证书可加载
	if _, err := tlspkg.LoadManual("", ""); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "证书加载失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "证书上传成功"})
}

// ---- IP 黑名单管理 ----

// ListIPBans IP 黑名单列表
func ListIPBans(c *gin.Context) {
	var bans []models.IPBan
	db.Get().Order("id desc").Find(&bans)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": bans})
}

type addIPBanReq struct {
	IP        string     `json:"ip" binding:"required"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// AddIPBan 手动添加 IP 封禁
func AddIPBan(c *gin.Context) {
	var req addIPBanReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	ban := models.IPBan{IP: req.IP, Reason: req.Reason, ExpiresAt: req.ExpiresAt, Auto: false}
	if err := db.Get().Create(&ban).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ban})
}

// DeleteIPBan 解除 IP 封禁
func DeleteIPBan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	db.Get().Delete(&models.IPBan{}, id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
