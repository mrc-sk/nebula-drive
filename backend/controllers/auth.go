package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/util"
	"github.com/nebula-drive/nebula/service"
	"golang.org/x/crypto/bcrypt"
)

type loginReq struct {
	UserName     string `json:"userName" binding:"required"`
	Password     string `json:"password" binding:"required"`
	Code         string `json:"code"`         // 2FA
	CaptchaToken string `json:"captchaToken"` // hCaptcha/Turnstile token
	Remember     bool   `json:"remember"`
}

// Login 登录（含 2FA、单设备登录、记住登录、验证码、登录失败封禁）
func Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	ip := c.ClientIP()
	// 验证码：开启 security.captcha_enabled 或连续失败 3 次后强制
	if getSettingBool("security.captcha_enabled", false) || middleware.RequireCaptcha(ip) {
		if !verifyCaptcha(req.CaptchaToken) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "requireCaptcha": true, "message": "需要完成验证码"})
			return
		}
	}
	var u models.User
	if err := db.Get().Where("user_name = ?", req.UserName).First(&u).Error; err != nil {
		middleware.RecordLoginFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "用户名或密码错误"})
		return
	}
	if u.Status != 0 {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "账号已被封禁"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		middleware.RecordLoginFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "用户名或密码错误"})
		return
	}
	// 两步验证
	if u.TwoFactor != "" {
		if req.Code == "" {
			c.JSON(http.StatusOK, gin.H{"code": 2, "message": "需要两步验证", "require2FA": true})
			return
		}
		if !service.ValidateTOTP(u.TwoFactor, req.Code) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "两步验证码错误"})
			return
		}
	}
	middleware.ClearLoginFailure(ip)
	ttl := time.Hour
	if req.Remember {
		ttl = 7 * 24 * time.Hour
	}
	sessID := util.UUID()
	tok, err := jwt.Sign(u.ID, sessID, ttl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 单设备登录：清除该用户其他会话
	db.Get().Where("user_id = ?", u.ID).Delete(&models.Session{})
	db.Get().Create(&models.Session{
		UserID: u.ID, Token: tok, SessionID: sessID,
		IP: c.ClientIP(), UA: c.Request.UserAgent(), ExpiresAt: time.Now().Add(ttl),
	})
	c.SetCookie("nebula_token", tok, int(ttl.Seconds()), "/", "", false, true)

	ctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"action":   "login",
	}
	plugin.Fire(plugin.HookRateLimit, ctx)

	db.Get().Create(&models.AuditLog{
		UserID:   u.ID,
		UserName: u.UserName,
		Action:   "login",
		Target:   "",
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   "sessionId=" + sessID,
	})

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"token": tok, "user": u}})
}

var _ = strconv.Itoa

// Logout 登出
func Logout(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if u != nil {
		db.Get().Where("user_id = ?", u.ID).Delete(&models.Session{})
	}
	c.SetCookie("nebula_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Me 当前用户
func Me(c *gin.Context) {
	u := middleware.CurrentUser(c)
	data := gin.H{"user": u}
	if u.TwoFactor == "" && !u.TwoFactorHinted {
		data["suggest2FAHint"] = true
		db.Get().Model(u).Update("two_factor_hinted", true)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
}

type updateProfileReq struct {
	PreferLang string `json:"preferLang"`
	ThemeMode  string `json:"themeMode"`
	LogoEgg    *bool  `json:"logoEgg"`
	SelectMode string `json:"selectMode"`
	NickName   string `json:"nickName"`
	Avatar     string `json:"avatar"`
}

// UpdateProfile 更新用户资料
func UpdateProfile(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req updateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	updates := map[string]any{}
	if req.PreferLang != "" {
		updates["prefer_lang"] = req.PreferLang
	}
	if req.ThemeMode != "" {
		updates["theme_mode"] = req.ThemeMode
	}
	if req.LogoEgg != nil {
		updates["logo_egg"] = *req.LogoEgg
	}
	if req.SelectMode != "" {
		updates["select_mode"] = req.SelectMode
	}
	if req.NickName != "" {
		updates["nick_name"] = req.NickName
	}
	if req.Avatar != "" {
		updates["avatar"] = req.Avatar
	}
	if len(updates) > 0 {
		db.Get().Model(u).Updates(updates)
		db.Get().First(u, u.ID)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": u})
}

type setup2FAReq struct {
	Enable bool   `json:"enable"`
	Code   string `json:"code"`
	Secret string `json:"secret"`
}

// Setup2FA 开启/关闭两步验证
func Setup2FA(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req setup2FAReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Enable {
		if u.TwoFactor != "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "已开启两步验证"})
			return
		}
		secret, err := service.NewTOTPSecret(u.UserName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"secret": secret}})
		return
	}
	// 关闭需验证码
	if !service.ValidateTOTP(u.TwoFactor, req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "两步验证码错误"})
		return
	}
	db.Get().Model(u).Update("two_factor", "")
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Confirm2FA 确认开启：前端带 secret + code，校验通过则绑定到用户
func Confirm2FA(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req setup2FAReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Secret == "" || !service.ValidateTOTP(req.Secret, req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "验证码错误"})
		return
	}
	db.Get().Model(u).Update("two_factor", req.Secret)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ChangePassword 修改密码
type changePwdReq struct {
	Old string `json:"old" binding:"required"`
	New string `json:"new" binding:"required"`
}

func ChangePassword(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req changePwdReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Old)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "原密码错误"})
		return
	}
	if err := validatePassword(req.New); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	db.Get().Model(u).Update("password", string(hash))
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 密码强度策略 ----

// validatePassword 按 settings 校验密码强度，不满足返回详细原因
func validatePassword(pwd string) error {
	minLen := getSettingInt("security.password_min_length", 8)
	if len(pwd) < minLen {
		return fmt.Errorf("密码长度至少 %d 位", minLen)
	}
	if getSettingBool("security.password_require_upper", true) && !strings.ContainsAny(pwd, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return errors.New("密码需包含大写字母")
	}
	if getSettingBool("security.password_require_digit", true) && !strings.ContainsAny(pwd, "0123456789") {
		return errors.New("密码需包含数字")
	}
	if getSettingBool("security.password_require_special", false) && !hasSpecialChar(pwd) {
		return errors.New("密码需包含特殊字符")
	}
	return nil
}

// hasSpecialChar 是否包含特殊字符
func hasSpecialChar(s string) bool {
	return strings.ContainsAny(s, "!@#$%^&*()-_=+[]{};:'\",.<>/?\\|`~")
}

// ---- 登录验证码 ----

// verifyCaptcha 校验 hCaptcha/Turnstile token。未配置 secret 时跳过（开发模式）
func verifyCaptcha(token string) bool {
	secret := strings.TrimSpace(getSetting("security.hcaptcha_secret"))
	if secret == "" {
		return true
	}
	if token == "" {
		return false
	}
	resp, err := http.PostForm("https://api.hcaptcha.com/siteverify", url.Values{
		"secret":   {secret},
		"response": {token},
	})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var r struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false
	}
	return r.Success
}

// ---- 公开注册 ----

type registerReq struct {
	UserName string `json:"userName" binding:"required"`
	Password string `json:"password" binding:"required"`
	Email    string `json:"email"`
}

// Register 公开注册（需后台开启 security.allow_register）
func Register(c *gin.Context) {
	if !getSettingBool("security.allow_register", false) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "未开放注册"})
		return
	}
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if err := validatePassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	var exist models.User
	if err := db.Get().Where("user_name = ?", req.UserName).First(&exist).Error; err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "用户名已存在"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	u := models.User{
		UserName: req.UserName, Email: req.Email, Password: string(hash),
		GroupID: 1, NickName: req.UserName,
	}
	if err := db.Get().Create(&u).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": u})
}
