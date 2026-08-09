package controllers

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// Preview 在线预览（inline 流式返回）
func Preview(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	u := middleware.CurrentUser(c)
	if !u.IsAdmin && f.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	if f.IsDir {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "cannot preview dir"})
		return
	}
	h, err := handlerForFile(&f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	rc, err := h.Get(f.SourceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer rc.Close()
	ct := f.MimeType
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Type", ct)
	c.Header("Content-Disposition", "inline")
	c.Header("Content-Length", strconv.FormatInt(f.Size, 10))
	io.Copy(c.Writer, rc)
}

// Thumb 缩略图：优先走 ThumbHandler（OSS/COS 图片处理或本地缓存），否则返回原图
func Thumb(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	u := middleware.CurrentUser(c)
	if !u.IsAdmin && f.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	if !strings.HasPrefix(f.MimeType, "image/") {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "not image"})
		return
	}
	h, err := handlerForFile(&f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	w, _ := strconv.Atoi(c.DefaultQuery("w", "300"))
	ht, _ := strconv.Atoi(c.DefaultQuery("h", "300"))

	var rc io.ReadCloser
	if th, ok := h.(filesystem.ThumbHandler); ok {
		rc, err = th.GetThumb(f.SourceName, w, ht)
	} else {
		rc, err = h.Get(f.SourceName)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer rc.Close()
	c.Header("Content-Type", f.MimeType)
	c.Header("Cache-Control", "public, max-age=86400")
	io.Copy(c.Writer, rc)
}
