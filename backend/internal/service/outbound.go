// Package service 收纳跨入口复用的业务逻辑与安全校验。
//
// 存在意义：历史上 HTTP 上传与 WebDAV 上传各自实现了一套校验，
// 后者遗漏了白名单 / 魔术字节 / 统一配额，导致同一存储层出现两套不等价的安全边界。
// 任何"每个写入口都必须做"的校验都应放在这里，由调用方强制复用。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---- 出站请求（SSRF）防护 ----

var (
	// ErrBlockedAddress 目标地址指向内网 / 环回 / 链路本地等禁止访问的网段
	ErrBlockedAddress = errors.New("blocked: target address is not allowed")
	// ErrBlockedScheme 非 http/https 协议
	ErrBlockedScheme = errors.New("blocked: only http/https scheme is allowed")
)

const (
	// outboundTimeout 出站请求总超时（连接 + 读取）
	outboundTimeout = 30 * time.Second
	// MaxOutboundResponseBytes 单次出站响应体读取默认上限（防止 io.ReadAll 打爆内存）
	MaxOutboundResponseBytes = 64 << 20 // 64MB
	// maxRedirects 允许的最大跳转次数（每次跳转都会重新校验目标）
	maxRedirects = 3
)

// IsForbiddenIP 判断 IP 是否落在禁止出站的网段。
// 覆盖：环回、私有网段、链路本地（含云元数据 169.254.169.254）、
// IPv6 ULA / 链路本地、未指定地址、多播、CGNAT，以及 IPv4 映射的 IPv6 形式。
func IsForbiddenIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// IPv4 映射的 IPv6（::ffff:127.0.0.1）还原后重新判断
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 0 || v4[0] == 127 || v4[0] == 10 ||
			(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) ||
			// 100.64.0.0/10 运营商级 NAT
			(v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) {
			return true
		}
	}
	return false
}

// ValidateOutboundURL 校验用户提交的 URL 是否可以作为出站目标。
// 只做协议 + 主机名解析后的 IP 判定；重定向校验由客户端 CheckRedirect 与 safeDialContext 兜住。
func ValidateOutboundURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrBlockedScheme
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("empty host")
	}
	// 主机名直接是 IP 字面量时先判一次
	if ip := net.ParseIP(host); ip != nil {
		if IsForbiddenIP(ip) {
			return nil, ErrBlockedAddress
		}
		return u, nil
	}
	// 域名：解析全部 A/AAAA 记录，任一落在禁止网段即拒绝
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return nil, errors.New("dns lookup returned no address")
	}
	for _, ip := range ips {
		if IsForbiddenIP(ip) {
			return nil, ErrBlockedAddress
		}
	}
	return u, nil
}

// safeDialContext 在 TCP 连接建立阶段再次校验目标 IP。
// 这一步不可省略：ValidateOutboundURL 之后 DNS 可能被重绑定（DNS Rebinding），
// 只有真正拨号时的地址才是可信的。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no address resolved")
	}
	var lastErr error
	for _, ipa := range ips {
		if IsForbiddenIP(ipa.IP) {
			lastErr = ErrBlockedAddress
			continue
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = ErrBlockedAddress
	}
	return nil, lastErr
}

// NewOutboundClient 构造启用了 SSRF 防护的 HTTP 客户端。
// 离线下载、种子文件抓取等一切"由用户提供 URL 的服务器出站请求"都必须用它。
func NewOutboundClient() *http.Client {
	return &http.Client{
		Timeout: outboundTimeout,
		Transport: &http.Transport{
			DialContext:           safeDialContext,
			DisableKeepAlives:     true,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			// 每次跳转都重新校验，防止 302 到内网
			if _, err := ValidateOutboundURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

var outboundClient = NewOutboundClient()

// OutboundClient 返回带 SSRF 防护的共享客户端
func OutboundClient() *http.Client { return outboundClient }

// OpenOutbound 校验并发起一次出站 GET，返回响应体（由调用方 Close）。
// 调用方负责按需再套一层大小限制。
func OpenOutbound(rawURL string) (io.ReadCloser, error) {
	if _, err := ValidateOutboundURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NebulaDrive/1.0")
	resp, err := outboundClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("remote returned status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// FetchBytes 下载用户提供的 URL 到内存，带完整 SSRF / 超时 / 大小防护。
// 仅用于小体积资源（如 .torrent 种子文件）。maxBytes<=0 时用 MaxOutboundResponseBytes。
func FetchBytes(rawURL string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = MaxOutboundResponseBytes
	}
	body, err := OpenOutbound(rawURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	// 多读 1 字节用于判断是否超限
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response exceeds size limit (%d bytes)", maxBytes)
	}
	return data, nil
}

// ---- 通用设置读取（带短 TTL 缓存，避免每个上传请求查 5 次库）----

// SettingGetter 由调用方注入从 DB 读设置的能力，避免 service 反向依赖 controllers。
// 返回 (value, found)。
type SettingGetter func(key string) (string, bool)

var settingGetter SettingGetter

// SetSettingGetter 由 main/bootstrap 注入设置读取实现
func SetSettingGetter(g SettingGetter) { settingGetter = g }

// GetSetting 读取设置原始字符串
func GetSetting(key, def string) string {
	if settingGetter == nil {
		return def
	}
	if v, ok := settingGetter(key); ok && v != "" {
		return v
	}
	return def
}

// GetSettingInt 读整数设置
func GetSettingInt(key string, def int) int {
	v := strings.TrimSpace(GetSetting(key, ""))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// GetSettingBool 读布尔设置
func GetSettingBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(GetSetting(key, ""))) {
	case "":
		return def
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// DefaultAllowedExtensions 默认允许上传的扩展名
func DefaultAllowedExtensions() string {
	return "jpg,jpeg,png,gif,webp,mp4,webm,mp3,wav,pdf,doc,docx,xls,xlsx,ppt,pptx,zip,rar,7z,tar,gz,txt,md,go,py,js,ts,json,yaml,yml,xml,csv"
}

// IsAllowedExtension 扩展名是否在上传白名单（HTTP 与 WebDAV 共用）
func IsAllowedExtension(ext string) bool {
	if ext == "" {
		return false
	}
	allowed := GetSetting("upload.allowed_extensions", "")
	if strings.TrimSpace(allowed) == "" {
		allowed = DefaultAllowedExtensions()
	}
	for _, e := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(e), ext) {
			return true
		}
	}
	return false
}

// IsImageExt 是否图片扩展名
func IsImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

// MagicCheckEnabled 是否启用魔术字节校验
func MagicCheckEnabled() bool {
	return GetSettingBool("upload.enable_magic_check", true)
}

// CheckImageMagic 校验声称为图片的内容是否真的是图片。head 至少 512 字节。
func CheckImageMagic(head []byte) bool {
	if len(head) == 0 {
		return false
	}
	return strings.HasPrefix(http.DetectContentType(head), "image/")
}

// ErrMagicMismatch 内容与声明的扩展名不符
var ErrMagicMismatch = errors.New("file content does not match its extension")

// CheckImageMagicErr 同 CheckImageMagic，但返回带说明的错误，便于直接回给客户端。
func CheckImageMagicErr(body []byte, ext string) error {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	if !CheckImageMagic(head) {
		return fmt.Errorf("%w: declared %s but content is %s", ErrMagicMismatch, ext, http.DetectContentType(head))
	}
	return nil
}

// MaxUploadBytes 单文件上传体积上限（HTTP 与 WebDAV 共用），默认 2GB。
// 设置项 upload.max_size_mb 可覆盖（单位 MB）。
func MaxUploadBytes() int64 {
	mb := GetSettingInt("upload.max_size_mb", 2048)
	if mb <= 0 {
		mb = 2048
	}
	return int64(mb) << 20
}

// QuotaSource 描述配额来自套餐还是用户组，用于错误信息
type QuotaSource struct {
	MaxStorage int64  // -1 表示无限
	Label      string // 形如 "group:default" / "plan:Pro"
}

// RateLimitInt 读限流配置的辅助（供 middleware 复用同一份读取逻辑）
func RateLimitInt(key string, def int) int { return GetSettingInt(key, def) }

// MarshalJSONOrNil 便于调试输出
func MarshalJSONOrNil(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
