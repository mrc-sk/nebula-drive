package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// ListNotifications 当前用户通知列表（分页）
func ListNotifications(c *gin.Context) {
	u := middleware.CurrentUser(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	db.Get().Model(&models.Notification{}).Where("user_id = ?", u.ID).Count(&total)
	var list []models.Notification
	db.Get().Where("user_id = ?", u.ID).Order("id desc").
		Offset((page - 1) * size).Limit(size).Find(&list)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"total": total, "list": list}})
}

// UnreadCount 未读数量
func UnreadCount(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var count int64
	db.Get().Model(&models.Notification{}).Where("user_id = ? AND `read` = ?", u.ID, false).Count(&count)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": count})
}

// MarkRead 标记单条已读
func MarkRead(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	res := db.Get().Model(&models.Notification{}).
		Where("id = ? AND user_id = ?", id, u.ID).Update("`read`", true)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": res.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// MarkAllRead 全部标记已读
func MarkAllRead(c *gin.Context) {
	u := middleware.CurrentUser(c)
	db.Get().Model(&models.Notification{}).
		Where("user_id = ? AND `read` = ?", u.ID, false).Update("`read`", true)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteNotification 删除通知
func DeleteNotification(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	db.Get().Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Notification{})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
