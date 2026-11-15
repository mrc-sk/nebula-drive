package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nebula-drive/nebula/internal/hooks"
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
	"gorm.io/gorm"
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
	// [plugin-hook] HookAuthLoginFailed（失败时）
	defer func() {
		if c.Writer.Status() >= 400 {
			hooks.Emit(plugin.HookAuthLoginFailed, hooks.ReqCtx(c, nil))
		}
	}()
	// [plugin-hook] HookAuthLoginSuccess（完成后）
	defer hooks.Emit(plugin.HookAuthLoginSuccess, hooks.ReqCtx(c, nil))
	// [plugin-hook] HookAuthTokenIssue（决策/变换，可拦截）
	if __ok, __why, _ := hooks.Gate(plugin.HookAuthTokenIssue, c, nil); !__ok {
		c.JSON(403, gin.H{"code": 403, "message": __why})
		return
	}
	// [plugin-hook] HookAuthProviderResolve（决策/变换，可拦截）
	if __ok, __why, _ := hooks.Gate(plugin.HookAuthProviderResolve, c, nil); !__ok {
		c.JSON(403, gin.H{"code": 403, "message": __why})
		return
	}
	// [plugin-hook] HookAuthBeforeLogin（决策/变换，可拦截）
	if __ok, __why, _ := hooks.Gate(plugin.HookAuthBeforeLogin, c, nil); !__ok {
		c.JSON(403, gin.H{"code": 403, "message": __why})
		return
	}
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
		if !service.ValidateTOTP(u.TwoFactor.String(), req.Code) {
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
	// [plugin-hook] HookAuthLogout
	hooks.Emit(plugin.HookAuthLogout, hooks.ReqCtx(c, nil))
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
	// [plugin-hook] HookUserUpdate（完成后）
	defer hooks.Emit(plugin.HookUserUpdate, hooks.ReqCtx(c, nil))
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
	// [plugin-hook] HookAuth2FAVerify（决策/变换，可拦截）
	if __ok, __why, _ := hooks.Gate(plugin.HookAuth2FAVerify, c, nil); !__ok {
		c.JSON(403, gin.H{"code": 403, "message": __why})
		return
	}
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
	if !service.ValidateTOTP(u.TwoFactor.String(), req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 1, "message": "两步验证码错误"})
		return
	}
	// 注意：值必须包成 models.From(...) 才能走 Encrypted 的 driver.Valuer 完成加密。
	// 直接传 string 会让 GORM 生成 `SET two_factor = <明文>`，hook 也拦不住（值来自 map 而非 struct）。
	db.Get().Model(u).Update("two_factor", models.From(""))
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
	// 同上：必须包成 models.From 才会加密落库
	db.Get().Model(u).Update("two_factor", models.From(req.Secret))
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ChangePassword 修改密码
type changePwdReq struct {
	Old string `json:"old" binding:"required"`
	New string `json:"new" binding:"required"`
}

func ChangePassword(c *gin.Context) {
	// [plugin-hook] HookAuthPasswordChange（完成后）
	defer hooks.Emit(plugin.HookAuthPasswordChange, hooks.ReqCtx(c, nil))
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

	// 改密码必须吊销所有已签发的会话。
	// 原因：会话有效性只由 sessions 表决定（middleware/auth.go 只查
	// session_id + expires_at），与密码完全解耦。不吊销的话，
	// 「我账号被入侵了所以改密码」这个补救动作对攻击者无效 ——
	// 他手上的 token 在「记住登录」的 7 天里照常可用。
	// 这是本项目里「单设备登录」机制的一个此前被忽略的入口。
	newToken, err := revokeAllSessions(c, u.ID, u.UserName)
	if err != nil {
		// 会话没清干净：密码已改但旧 token 仍有效，如实报错而不是假装成功
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "密码已修改，但吊销其他登录会话失败，请检查服务端日志"})
		return
	}
	// 给当前浏览器换发新 token，使其在清空会话后继续可用（无感）
	if newToken != "" {
		c.SetCookie("nebula_token", newToken, int((7 * 24 * time.Hour).Seconds()), "/", "", false, true)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// revokeAllSessions 删除该用户全部会话并为当前请求签发一个新会话。
// 返回新 token；失败时返回 error 且新 token 为空。
//
// 为什么全删而不是「只删其他会话」：改密码的语义是「我认为账号可能已泄露」。
// 保留任何旧 token 都留了后门。当前请求通过新签发的 token 续上，用户无感。
func revokeAllSessions(c *gin.Context, userID uint, userName string) (string, error) {
	if err := db.Get().Where("user_id = ?", userID).Delete(&models.Session{}).Error; err != nil {
		return "", err
	}
	sessID := util.UUID()
	tok, err := jwt.Sign(userID, sessID, 7*24*time.Hour)
	if err != nil {
		return "", err
	}
	if err := db.Get().Create(&models.Session{
		UserID: userID, Token: tok, SessionID: sessID,
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}).Error; err != nil {
		return "", err
	}
	// 审计：这类操作必须留痕
	db.Get().Create(&models.AuditLog{
		UserID: userID, UserName: userName,
		Action: "change_password", Target: strconv.Itoa(int(userID)),
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
		Detail: "all sessions revoked",
	})
	return tok, nil
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
	UserName   string `json:"userName" binding:"required"`
	Password   string `json:"password" binding:"required"`
	Email      string `json:"email"`
	InviteCode string `json:"inviteCode"` // 注册邀请码（后台生成、未失效）
}

// Register 公开注册。
//
// 两条通路：
//   - 自由注册：后台开启 security.allow_register 时，无需邀请码即可注册（沿用旧行为）。
//   - 邀请码注册：提交后台已创建且未失效的邀请码即可注册；即使自由注册关闭也放行，
//     这样站点可以「关闭自由注册、只凭邀请码进人」。
//
// 邀请码校验通过后，按该码绑定的套餐类型（RedemptionCode.PlanID）给新注册用户
// 分配初始配额 / 套餐 / 订阅，累计使用次数、用尽自动停用、写审计日志与兑换流水，
// 即「用完看该码类型做处理」。
//
// 注册表单字段通过 hooks.Gate 的 extras 透传给 onAuthRegisterBefore 钩子，
// 让注册码类插件（如 nd-invite-code）也能读到 inviteCode 做二次校验。
func Register(c *gin.Context) {
	// [plugin-hook] HookAuthRegisterAfter（完成后）
	defer hooks.Emit(plugin.HookAuthRegisterAfter, hooks.ReqCtx(c, nil))

	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// [plugin-hook] HookAuthRegisterBefore（决策/变换，可拦截）
	// 透传注册表单字段，供插件校验 / 变换；不把密码放进 ctx。
	if __ok, __why, _ := hooks.Gate(plugin.HookAuthRegisterBefore, c, map[string]any{
		"userName": req.UserName, "email": req.Email, "inviteCode": req.InviteCode,
	}); !__ok {
		c.JSON(403, gin.H{"code": 403, "message": __why})
		return
	}

	inviteCode := strings.ToUpper(strings.TrimSpace(req.InviteCode))
	requireInvite := getSettingBool("security.require_invite_code", false)
	allowFree := getSettingBool("security.allow_register", false)

	var invite *models.RedemptionCode
	var plan *models.Plan
	if inviteCode != "" {
		var rc models.RedemptionCode
		if err := db.Get().Where("code = ?", inviteCode).First(&rc).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "邀请码不存在"})
			return
		}
		if !rc.IsActive {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "邀请码已失效"})
			return
		}
		if rc.ExpiresAt != nil && rc.ExpiresAt.Before(time.Now()) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "邀请码已过期"})
			return
		}
		if rc.MaxUses > 0 && rc.UsedCount >= rc.MaxUses {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "邀请码使用次数已达上限"})
			return
		}
		var p models.Plan
		if err := db.Get().Where("id = ? AND is_active = ?", rc.PlanID, true).First(&p).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "邀请码对应的套餐已下架，请联系管理员"})
			return
		}
		invite, plan = &rc, &p
	} else {
		// 未填邀请码：要么强制需要邀请码，要么自由注册已开启，否则拒绝
		if requireInvite {
			c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "本站需凭邀请码注册，请填写有效邀请码"})
			return
		}
		if !allowFree {
			c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "未开放注册，请联系管理员获取邀请码"})
			return
		}
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
	// 用事务把「建用户 + 按码类型处理 + 累计次数」绑在一起，避免半途失败产生脏数据。
	err := db.Get().Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&u).Error; e != nil {
			return e
		}
		if invite == nil || plan == nil {
			return nil
		}
		now := time.Now()
		end := now.AddDate(0, plan.DurationMonths, 0)
		sub := models.Subscription{
			UserID: u.ID, UserName: u.UserName, PlanID: plan.ID, PlanName: plan.DisplayName,
			CodeID: invite.ID, Code: invite.Code, StartTime: now, EndTime: end, Status: "active",
		}
		if e := tx.Create(&sub).Error; e != nil {
			return e
		}
		if e := tx.Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{
			"plan_id": plan.ID, "plan_expire_at": end,
		}).Error; e != nil {
			return e
		}
		nextUsed := invite.UsedCount + 1
		updates := map[string]any{"used_count": nextUsed}
		if invite.MaxUses > 0 && nextUsed >= invite.MaxUses {
			updates["is_active"] = false // 次数用尽自动失效
		}
		if e := tx.Model(&models.RedemptionCode{}).Where("id = ?", invite.ID).Updates(updates).Error; e != nil {
			return e
		}
		if e := tx.Create(&models.RedemptionLog{
			CodeID: invite.ID, Code: invite.Code, UserID: u.ID, UserName: u.UserName,
			PlanID: plan.ID, PlanName: plan.DisplayName,
			IP: c.ClientIP(), UA: c.Request.UserAgent(),
		}).Error; e != nil {
			return e
		}
		return tx.Create(&models.AuditLog{
			UserID: u.ID, UserName: u.UserName, Action: "register_with_invite",
			Target: "code=" + invite.Code,
			IP:     c.ClientIP(), UA: c.Request.UserAgent(),
			Detail: "plan=" + plan.DisplayName,
		}).Error
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": u})
}

// RegisterMode 公开查询注册策略，供登录页决定是否显示「注册 / 邀请码」入口。
// 无需登录，不泄露任何敏感配置。
func RegisterMode(c *gin.Context) {
	var plans []models.Plan
	if db.Get() != nil {
		db.Get().Where("is_active = ?", true).Order("sort_order asc, id asc").Find(&plans)
	}
	list := make([]gin.H, 0, len(plans))
	for _, p := range plans {
		list = append(list, gin.H{
			"id": p.ID, "name": p.Name, "displayName": p.DisplayName,
			"durationMonths": p.DurationMonths, "maxStorage": p.MaxStorage,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"allowRegister":     getSettingBool("security.allow_register", false),
			"requireInviteCode": getSettingBool("security.require_invite_code", false),
			"plans":             list,
		},
	})
}
