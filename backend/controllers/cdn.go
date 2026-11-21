package controllers

// CDN 加速站点：把文件下载/预览改成 302 到配置好的 CDN 域名，
// CDN 再从一个带短时效 HMAC 签名的回源端点拉取真实内容。
//
// 设计边界（改这个文件前先读）：
//
//   - 签名 URL 是 bearer capability：拿到链接的人在该时效内可读到文件。
//     这跟对象存储 PresignGet（streamFile 里已有的 5 分钟 302）语义一致。
//     因此 TTL 默认只有 5 分钟，且 302 响应设 no-store。
//   - 回源端点 /cdn/files/:id 必须免鉴权（CDN 不会带 Bearer），
//     但用 HMAC 签名代替登录态，且校验时间窗防重放。签名用常量时间比较。
//   - 只有配置了 cdn.secret 才启用。secret 为空时 CDN 一律不生效，
//     fail closed —— 绝不允许"开了开关但没密钥"导致裸奔回源。
//   - CDN 回源域名不能是站点自身的 CDN 域名，否则形成回源死循环。
//     文档里要求管理员把回源指向真实源站；这里只做 URL scheme 校验。

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

const cdnDefaultTTL = 5 * time.Minute // 与对象存储 PresignGet 对齐

// cdnSecret 从 settings KV 读签名密钥。空白或过短都视为未配置。
// 至少要 16 字节，避免有人用 "1" 这种密钥把 capability URL 变成可爆破的。
func cdnSecret() string {
	s := strings.TrimSpace(getSetting("cdn.secret"))
	if len(s) < 16 {
		return ""
	}
	return s
}

// cdnEnabled 判断 CDN 是否可用：开关打开 + 域名合法 + 密钥存在，三者缺一不可。
func cdnEnabled() (bool, string) {
	if !getSettingBool("cdn.enabled", false) {
		return false, ""
	}
	secret := cdnSecret()
	if secret == "" {
		return false, ""
	}
	domain := strings.TrimRight(strings.TrimSpace(getSetting("cdn.domain")), "/")
	if domain == "" {
		return false, ""
	}
	// 只允许 http/https，杜绝 javascript:/data: 之类被塞进 Location。
	u, err := url.Parse(domain)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false, ""
	}
	return true, domain
}

// cdnSign 计算签名：HMAC-SHA256(secret, "cdn:{id}:{exp}:{disp}") 的十六进制。
// id、exp、disp 都进签名，改任何一个都会失效；不做 path 拼接，避免歧义。
// disp 必须是 inline 或 attachment —— 下载要弹出保存框，预览要内联，
// 回源时必须还原这个语义，否则 CDN 加速后"下载"会变成直接在浏览器打开。
func cdnSign(secret string, fileID uint, exp int64, disp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "cdn:%d:%d:%s", fileID, exp, disp)
	return hex.EncodeToString(mac.Sum(nil))
}

// cdnURL 生成一个短时效的 CDN 签名 URL（指向回源端点）。
func cdnURL(domain string, secret string, fileID uint, disp string) string {
	if disp != "inline" && disp != "attachment" {
		disp = "attachment"
	}
	// 管理员可能填带尾斜杠的域名，去掉避免 //cdn/files 这种双斜杠路径。
	domain = strings.TrimRight(strings.TrimSpace(domain), "/")
	exp := time.Now().Add(cdnDefaultTTL).Unix()
	sig := cdnSign(secret, fileID, exp, disp)
	return fmt.Sprintf("%s/cdn/files/%d?exp=%d&disp=%s&sig=%s", domain, fileID, exp, disp, sig)
}

// maybeCDNRedirect 在 streamFile 的最前面调用。
// CDN 启用时 302 到签名 URL；否则什么都不做，走原本的直出/对象存储 Presign 逻辑。
//
// 返回 true 表示已经写出响应，调用方应立即 return。
func maybeCDNRedirect(c *gin.Context, f *models.File, disp string) bool {
	// 回源请求本身绝不能再跳 CDN，否则形成 302 死循环。
	if _, inOrigin := c.Get("cdn_origin"); inOrigin {
		return false
	}
	ok, domain := cdnEnabled()
	if !ok {
		return false
	}
	secret := cdnSecret()
	target := cdnURL(domain, secret, f.ID, disp)

	// capability URL 本身绝不能被中间缓存持久化；CDN 侧应尊重这个头，
	// 只缓存回源结果、不缓存 302 跳转本身。
	c.Header("Cache-Control", "private, no-store")
	c.Header("Location", target)
	c.Status(http.StatusFound)
	return true
}

// CDNOrigin 是 CDN 的回源端点：免登录，但必须带合法签名且在时间窗内。
// 校验通过后复用 streamFile —— 那正是所有鉴权已完成之后的统一内容出口。
func CDNOrigin(c *gin.Context) {
	// 先做配置/参数校验，任何失败都 fail closed。
	ok, _ := cdnEnabled()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
		return
	}

	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "bad file id"})
		return
	}
	exp, err := strconv.ParseInt(c.Query("exp"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "bad exp"})
		return
	}
	sig := strings.TrimSpace(c.Query("sig"))
	disp := c.Query("disp")
	if disp != "inline" && disp != "attachment" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "bad disp"})
		return
	}

	now := time.Now().Unix()
	// 时间窗：过期不接受，也拒绝未来时间太远的（防止签名被提前生成后长期有效）。
	if exp < now-30 || exp > now+int64(cdnDefaultTTL.Seconds())+30 {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "link expired"})
		return
	}

	secret := cdnSecret()
	want := cdnSign(secret, uint(id64), exp, disp)
	// 常量时间比较，避免通过响应耗时逐字节猜签名。
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "bad signature"})
		return
	}

	// 签名合法 → 走到这里的内容与登录下载完全一致。
	var f models.File
	if err := db.Get().First(&f, id64).Error; err != nil || f.IsDir {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
		return
	}
	h, herr := handlerForFile(&f)
	if herr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": herr.Error()})
		return
	}

	// 回源响应允许 CDN 缓存，但缓存时间不得超过签名 TTL，
	// 否则签名失效后 CDN 仍拿旧缓存对外服务。
	c.Set("cdn_origin", true) // 阻止 streamFile 再次跳 CDN
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", int(cdnDefaultTTL.Seconds())))
	streamFile(c, &f, h, disp)
}
