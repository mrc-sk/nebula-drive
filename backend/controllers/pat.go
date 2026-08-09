package controllers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/util"
)

const patTokenPrefix = "nd_pat_"

type createPATReq struct {
	Name      string     `json:"name" binding:"required"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// CreatePAT 创建个人访问令牌（完整 token 仅返回一次）
func CreatePAT(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req createPATReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Scope == "" {
		req.Scope = "profile"
	}
	full := patTokenPrefix + util.RandomStr(32)
	pat := models.PersonalAccessToken{
		UserID:    u.ID,
		Name:      req.Name,
		Token:     full,
		Prefix:    full[:len(patTokenPrefix)+8],
		Scope:     req.Scope,
		ExpiresAt: req.ExpiresAt,
	}
	if err := db.Get().Create(&pat).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 完整 token 仅本次返回，之后只显示 prefix
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"id":        pat.ID,
		"name":      pat.Name,
		"token":     full,
		"prefix":    pat.Prefix,
		"scope":     pat.Scope,
		"expiresAt": pat.ExpiresAt,
		"createdAt": pat.CreatedAt,
	}})
}

// ListPATs 列出我的 PAT（不返回完整 token）
func ListPATs(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var pats []models.PersonalAccessToken
	db.Get().Where("user_id = ?", u.ID).Order("id desc").Find(&pats)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": pats})
}

// DeletePAT 撤销 PAT
func DeletePAT(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	res := db.Get().Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.PersonalAccessToken{})
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
