package controllers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// ============ 套餐管理（管理员）============

// ListPlans 套餐列表
func ListPlans(c *gin.Context) {
	var plans []models.Plan
	db.Get().Order("sort_order asc, id asc").Find(&plans)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": plans})
}

type planReq struct {
	Name           string `json:"name" binding:"required"`
	DisplayName    string `json:"displayName"`
	Price          int    `json:"price"`
	Currency       string `json:"currency"`
	DurationMonths int    `json:"durationMonths"`
	MaxStorage     int64  `json:"maxStorage"`
	ShareEnabled   bool   `json:"shareEnabled"`
	WebDAVEnabled  bool   `json:"webdavEnabled"`
	SpeedLimit     int    `json:"speedLimit"`
	Features       string `json:"features"`
	IsActive       bool   `json:"isActive"`
	SortOrder      int    `json:"sortOrder"`
}

// CreatePlan 创建套餐
func CreatePlan(c *gin.Context) {
	var req planReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Currency == "" {
		req.Currency = "CNY"
	}
	p := models.Plan{
		Name: req.Name, DisplayName: req.DisplayName, Price: req.Price, Currency: req.Currency,
		DurationMonths: req.DurationMonths, MaxStorage: req.MaxStorage,
		ShareEnabled: req.ShareEnabled, WebDAVEnabled: req.WebDAVEnabled, SpeedLimit: req.SpeedLimit,
		Features: req.Features, IsActive: req.IsActive, SortOrder: req.SortOrder,
	}
	if err := db.Get().Create(&p).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": p})
}

// UpdatePlan 更新套餐
func UpdatePlan(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid plan id"})
		return
	}
	var req planReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	tx := db.Get().Model(&models.Plan{}).Where("id = ?", uint(id)).Updates(map[string]any{
		"name":            req.Name,
		"display_name":    req.DisplayName,
		"price":           req.Price,
		"currency":        req.Currency,
		"duration_months": req.DurationMonths,
		"max_storage":     req.MaxStorage,
		"share_enabled":   req.ShareEnabled,
		"web_dav_enabled": req.WebDAVEnabled,
		"speed_limit":     req.SpeedLimit,
		"features":        req.Features,
		"is_active":       req.IsActive,
		"sort_order":      req.SortOrder,
	})
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": tx.Error.Error()})
		return
	}
	if tx.RowsAffected == 0 {
		var cnt int64
		db.Get().Model(&models.Plan{}).Where("id = ?", uint(id)).Count(&cnt)
		if cnt == 0 {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "plan not found"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeletePlan 删除套餐（仅无活跃订阅时）
func DeletePlan(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var cnt int64
	db.Get().Model(&models.Subscription{}).Where("plan_id = ? AND status = 'active'", uint(id)).Count(&cnt)
	if cnt > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "该套餐仍有活跃订阅，无法删除"})
		return
	}
	db.Get().Delete(&models.Plan{}, id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ============ 兑换码管理（管理员）============

// ListCodes 兑换码列表
func ListCodes(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q := db.Get().Model(&models.RedemptionCode{})
	if planID := c.Query("planId"); planID != "" {
		q = q.Where("plan_id = ?", planID)
	}
	if active := c.Query("active"); active == "true" {
		q = q.Where("is_active = ?", true)
	}
	q.Count(&total)
	var codes []models.RedemptionCode
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&codes)
	// 附带套餐名
	planMap := make(map[uint]string)
	var plans []models.Plan
	db.Get().Find(&plans)
	for _, p := range plans {
		planMap[p.ID] = p.DisplayName
	}
	type codeWithPlan struct {
		models.RedemptionCode
		PlanName string `json:"planName"`
	}
	result := make([]codeWithPlan, len(codes))
	for i, c := range codes {
		result[i].RedemptionCode = c
		result[i].PlanName = planMap[c.PlanID]
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"total": total, "list": result}})
}

type generateCodesReq struct {
	PlanID   uint   `json:"planId" binding:"required"`
	Count    int    `json:"count" binding:"required"`
	MaxUses  int    `json:"maxUses"`  // 0=不限次数
	Note     string `json:"note"`
	ExpireAt string `json:"expireAt"` // RFC3339 可选
}

// generateCode 生成 NB-XXXX-XXXX-XXXX 格式兑换码
func generateCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去掉易混淆字符
	seg := func() string {
		b := make([]byte, 4)
		for i := range b {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			b[i] = charset[n.Int64()]
		}
		return string(b)
	}
	return fmt.Sprintf("NB-%s-%s-%s", seg(), seg(), seg())
}

// GenerateCodes 批量生成兑换码
func GenerateCodes(c *gin.Context) {
	var req generateCodesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Count < 1 || req.Count > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "count 范围 1-1000"})
		return
	}
	// 验证套餐存在
	var plan models.Plan
	if err := db.Get().Where("id = ?", req.PlanID).First(&plan).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "套餐不存在"})
		return
	}
	cur := middleware.CurrentUser(c)
	var expireAt *time.Time
	if req.ExpireAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpireAt)
		if err == nil {
			expireAt = &t
		}
	}
	codes := make([]string, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		code := generateCode()
		rc := models.RedemptionCode{
			Code: code, PlanID: req.PlanID, MaxUses: req.MaxUses,
			IsActive: true, CreatedBy: cur.ID, Note: req.Note, ExpiresAt: expireAt,
		}
		if err := db.Get().Create(&rc).Error; err != nil {
			// 唯一冲突极小概率，重试一次
			code = generateCode()
			rc.Code = code
			if err2 := db.Get().Create(&rc).Error; err2 != nil {
				continue
			}
		}
		codes = append(codes, code)
	}
	// 审计日志
	detail, _ := json.Marshal(req)
	db.Get().Create(&models.AuditLog{
		UserID: cur.ID, UserName: cur.UserName, Action: "admin_generate_codes",
		Target: fmt.Sprintf("plan=%d count=%d", req.PlanID, len(codes)),
		IP: c.ClientIP(), UA: c.Request.UserAgent(), Detail: string(detail),
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": codes, "generated": len(codes)})
}

// DisableCode 禁用兑换码
func DisableCode(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	tx := db.Get().Model(&models.RedemptionCode{}).Where("id = ?", uint(id)).Update("is_active", false)
	if tx.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "兑换码不存在"})
		return
	}
	cur := middleware.CurrentUser(c)
	db.Get().Create(&models.AuditLog{
		UserID: cur.ID, UserName: cur.UserName, Action: "admin_disable_code",
		Target: c.Param("id"), IP: c.ClientIP(), UA: c.Request.UserAgent(),
	})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteCode 删除兑换码
func DeleteCode(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	db.Get().Delete(&models.RedemptionCode{}, id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ============ 订阅管理（管理员）============

// ListSubscriptions 订阅列表
func ListSubscriptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q := db.Get().Model(&models.Subscription{})
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	if uid := c.Query("userId"); uid != "" {
		q = q.Where("user_id = ?", uid)
	}
	q.Count(&total)
	var subs []models.Subscription
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&subs)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"total": total, "list": subs}})
}

// ListRedemptionLogs 兑换记录
func ListRedemptionLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	q := db.Get().Model(&models.RedemptionLog{})
	if code := c.Query("code"); code != "" {
		q = q.Where("code = ?", code)
	}
	q.Count(&total)
	var logs []models.RedemptionLog
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&logs)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"total": total, "list": logs}})
}

// ============ 用户端 ============

// GetPlans 公开套餐列表（用户可见）
func GetPlans(c *gin.Context) {
	var plans []models.Plan
	db.Get().Where("is_active = ?", true).Order("sort_order asc, id asc").Find(&plans)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": plans})
}

// GetMySubscription 当前用户订阅状态
func GetMySubscription(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var sub models.Subscription
	hasActive := false
	if u.PlanID > 0 {
		if err := db.Get().Where("user_id = ? AND plan_id = ? AND status = 'active'",
			u.ID, u.PlanID).Order("end_time desc").First(&sub).Error; err == nil {
			if sub.EndTime.After(time.Now()) {
				hasActive = true
			}
		}
	}
	// 所有兑换历史
	var history []models.Subscription
	db.Get().Where("user_id = ?", u.ID).Order("id desc").Limit(20).Find(&history)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"hasActive":     hasActive,
			"current":       sub,
			"planId":        u.PlanID,
			"planExpireAt":  u.PlanExpireAt,
			"history":       history,
		},
	})
}

type redeemReq struct {
	Code string `json:"code" binding:"required"`
}

// RedeemCode 兑换码兑换
func RedeemCode(c *gin.Context) {
	var req redeemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请输入兑换码"})
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if !strings.HasPrefix(code, "NB-") {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "兑换码格式错误"})
		return
	}

	u := middleware.CurrentUser(c)

	// 查找兑换码
	var rc models.RedemptionCode
	if err := db.Get().Where("code = ?", code).First(&rc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "兑换码不存在"})
		return
	}
	if !rc.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "兑换码已失效"})
		return
	}
	if rc.ExpiresAt != nil && rc.ExpiresAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "兑换码已过期"})
		return
	}
	if rc.MaxUses > 0 && rc.UsedCount >= rc.MaxUses {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "兑换码使用次数已达上限"})
		return
	}

	// 查找套餐
	var plan models.Plan
	if err := db.Get().Where("id = ?", rc.PlanID).First(&plan).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "套餐不存在"})
		return
	}
	if !plan.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "套餐已下架"})
		return
	}

	// 计算订阅时间
	now := time.Now()
	startTime := now
	// 如果用户已有活跃订阅，叠加时长
	if u.PlanID > 0 && u.PlanExpireAt != nil && u.PlanExpireAt.After(now) {
		startTime = *u.PlanExpireAt
	}
	endTime := startTime.AddDate(0, plan.DurationMonths, 0)

	// 创建订阅记录
	sub := models.Subscription{
		UserID: u.ID, UserName: u.UserName, PlanID: plan.ID, PlanName: plan.DisplayName,
		CodeID: rc.ID, Code: rc.Code, StartTime: startTime, EndTime: endTime,
		Status: "active",
	}
	if err := db.Get().Create(&sub).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "创建订阅失败: " + err.Error()})
		return
	}

	// 更新用户当前套餐
	db.Get().Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{
		"plan_id":       plan.ID,
		"plan_expire_at": endTime,
	})

	// 更新兑换码使用次数
	db.Get().Model(&rc).Where("id = ?", rc.ID).UpdateColumn("used_count", rc.UsedCount+1)

	// 创建兑换审计日志
	db.Get().Create(&models.RedemptionLog{
		CodeID: rc.ID, Code: rc.Code, UserID: u.ID, UserName: u.UserName,
		PlanID: plan.ID, PlanName: plan.DisplayName,
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
	})

	// 创建通知
	CreateNotification(u.ID, "套餐兑换成功",
		fmt.Sprintf("您已成功兑换 %s 套餐，有效期至 %s", plan.DisplayName, endTime.Format("2006-01-02")),
		"success", "system")

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"message": fmt.Sprintf("兑换成功！%s 套餐有效期至 %s", plan.DisplayName, endTime.Format("2006-01-02")),
		"data": gin.H{
			"planName":  plan.DisplayName,
			"startTime": startTime,
			"endTime":   endTime,
		},
	})
}

// ============ 默认套餐初始化 ============

// SeedDefaultPlans 创建默认三档套餐（仅首次启动）
func SeedDefaultPlans() {
	var cnt int64
	db.Get().Model(&models.Plan{}).Count(&cnt)
	if cnt > 0 {
		return
	}
	plans := []models.Plan{
		{Name: "ultra", DisplayName: "Ultra", Price: 5900, Currency: "CNY", DurationMonths: 12,
			MaxStorage: 10737418240, ShareEnabled: true, WebDAVEnabled: true, SpeedLimit: 0,
			IsActive: true, SortOrder: 1},
		{Name: "pro", DisplayName: "Pro", Price: 10000, Currency: "CNY", DurationMonths: 12,
			MaxStorage: 53687091200, ShareEnabled: true, WebDAVEnabled: true, SpeedLimit: 0,
			IsActive: true, SortOrder: 2},
		{Name: "promax", DisplayName: "Pro Max", Price: 100000, Currency: "CNY", DurationMonths: 36,
			MaxStorage: -1, ShareEnabled: true, WebDAVEnabled: true, SpeedLimit: 0,
			IsActive: true, SortOrder: 3},
	}
	for _, p := range plans {
		db.Get().Create(&p)
	}
}
