package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/util"
)

const (
	oauthCodeTTL    = 10 * time.Minute
	oauthTokenTTL   = 2 * time.Hour
	oauthTokenScope = "profile"
)

type createOAuthAppReq struct {
	Name         string   `json:"name" binding:"required"`
	RedirectURIs []string `json:"redirectUris"`
}

// CreateOAuthApp 创建 OAuth App（管理员）
func CreateOAuthApp(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req createOAuthAppReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	uris, _ := json.Marshal(req.RedirectURIs)
	app := models.OAuthApp{
		ClientID:     "nd_" + util.RandomStr(16),
		ClientSecret: util.RandomStr(32),
		Name:         req.Name,
		RedirectURIs: string(uris),
		UserID:       u.ID,
	}
	if err := db.Get().Create(&app).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 完整 secret 仅创建时返回一次
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"id":           app.ID,
		"clientId":     app.ClientID,
		"clientSecret": app.ClientSecret,
		"name":         app.Name,
		"redirectUris": req.RedirectURIs,
		"userId":       app.UserID,
		"createdAt":    app.CreatedAt,
	}})
}

// ListOAuthApps 列出我的 OAuth Apps
func ListOAuthApps(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var apps []models.OAuthApp
	db.Get().Where("user_id = ?", u.ID).Order("id desc").Find(&apps)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": apps})
}

// DeleteOAuthApp 删除 OAuth App
func DeleteOAuthApp(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	res := db.Get().Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.OAuthApp{})
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// oauthAppRedirectURIs 解析 app 的 redirectURIs JSON
func oauthAppRedirectURIs(app *models.OAuthApp) []string {
	var uris []string
	_ = json.Unmarshal([]byte(app.RedirectURIs), &uris)
	return uris
}

// OAuthAuthorize 授权确认页（返回 app 信息让前端展示确认页）
func OAuthAuthorize(c *gin.Context) {
	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	scope := c.Query("scope")
	state := c.Query("state")
	responseType := c.Query("response_type")

	var app models.OAuthApp
	if err := db.Get().Where("client_id = ?", clientID).First(&app).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "invalid client_id"})
		return
	}
	uris := oauthAppRedirectURIs(&app)
	if len(uris) > 0 && redirectURI != "" {
		matched := false
		for _, u := range uris {
			if u == redirectURI {
				matched = true
				break
			}
		}
		if !matched {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "redirect_uri not registered"})
			return
		}
	}
	if responseType != "code" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "unsupported response_type"})
		return
	}
	if scope == "" {
		scope = oauthTokenScope
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"appId":       app.ID,
		"clientId":    app.ClientID,
		"appName":     app.Name,
		"redirectUri": redirectURI,
		"scope":       scope,
		"state":       state,
	}})
}

type oauthAuthorizeConfirmReq struct {
	ClientID    string `json:"client_id" binding:"required"`
	RedirectURI string `json:"redirect_uri"`
	Scope       string `json:"scope"`
	State       string `json:"state"`
	Deny        bool   `json:"deny"`
}

// OAuthAuthorizeConfirm 用户确认授权，生成 code
func OAuthAuthorizeConfirm(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req oauthAuthorizeConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	var app models.OAuthApp
	if err := db.Get().Where("client_id = ?", req.ClientID).First(&app).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "invalid client_id"})
		return
	}
	if req.Deny {
		redirect := buildRedirect(req.RedirectURI, "", "", "access_denied", req.State)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"redirect": redirect}})
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = oauthTokenScope
	}
	code := util.RandomStr(32)
	oc := models.OAuthCode{
		Code:        code,
		AppID:       app.ID,
		UserID:      u.ID,
		Scope:       scope,
		RedirectURI: req.RedirectURI,
		ExpiresAt:   time.Now().Add(oauthCodeTTL),
	}
	if err := db.Get().Create(&oc).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	redirect := buildRedirect(req.RedirectURI, code, req.State, "", "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"redirect": redirect,
		"code":     code,
	}})
}

func buildRedirect(base, code, state, errCode, stateErr string) string {
	if base == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	q := ""
	if code != "" {
		q += "code=" + code
	}
	if state != "" {
		if q != "" {
			q += "&"
		}
		q += "state=" + state
	}
	if errCode != "" {
		if q != "" {
			q += "&"
		}
		q += "error=" + errCode
	}
	if stateErr != "" {
		if q != "" {
			q += "&"
		}
		q += "state=" + stateErr
	}
	return base + sep + q
}

type oauthTokenReq struct {
	GrantType    string `json:"grant_type" form:"grant_type"`
	Code         string `json:"code" form:"code"`
	RedirectURI  string `json:"redirect_uri" form:"redirect_uri"`
	ClientID     string `json:"client_id" form:"client_id"`
	ClientSecret string `json:"client_secret" form:"client_secret"`
}

// OAuthToken 用 code 换 access_token（标准 OAuth2 token endpoint）
func OAuthToken(c *gin.Context) {
	var req oauthTokenReq
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	if req.GrantType != "authorization_code" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
		return
	}
	var app models.OAuthApp
	if err := db.Get().Where("client_id = ? AND client_secret = ?", req.ClientID, req.ClientSecret).First(&app).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client"})
		return
	}
	var oc models.OAuthCode
	err := db.Get().Where("code = ? AND app_id = ?", req.Code, app.ID).First(&oc).Error
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "code not found"})
		return
	}
	if oc.Used {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "code already used"})
		return
	}
	if oc.ExpiresAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "code expired"})
		return
	}
	if oc.RedirectURI != "" && req.RedirectURI != "" && oc.RedirectURI != req.RedirectURI {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "redirect_uri mismatch"})
		return
	}
	db.Get().Model(&oc).Update("used", true)

	token := util.RandomStr(32)
	at := models.AccessToken{
		Token:     token,
		UserID:    oc.UserID,
		AppID:     app.ID,
		Scope:     oc.Scope,
		ExpiresAt: time.Now().Add(oauthTokenTTL),
	}
	if err := db.Get().Create(&at).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int64(oauthTokenTTL.Seconds()),
		"scope":        at.Scope,
	})
}

// OAuthUserInfo 用 access_token 获取用户信息
func OAuthUserInfo(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"id":       u.ID,
		"userName": u.UserName,
		"email":    u.Email,
		"nickName": u.NickName,
		"avatar":   u.Avatar,
		"isAdmin":  u.IsAdmin,
	}})
}
