package controllers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// 插件签名与信任管理。
//
// # 为什么需要这组接口
//
// 签名本身在 pbclus 包里实现了，但"谁来建立信任"需要一个入口。
// 之前的做法是把 `plugin-trust.json` 手工放进数据目录 —— 能用，
// 但有两个实际问题：
//
//  1. **管理员看不到现状**：不知道当前信任了谁、策略是什么；
//  2. **改策略要手改配置文件**：写错一个值（比如 `Require` 大写）
//     会按 fail closed 处理，表现为"插件突然都装不上"，而原因很难找。
//
// 所以这里提供查询/新增/移除/改策略四个动作，全部走管理端并落审计。
//
// # 一条硬规则：接口只接受**公钥**，不接受私钥
//
// 发布者的私钥文件里同时含私钥与公钥。若接口允许"把密钥文件贴进来"，
// 就会鼓励一种危险的传递方式。因此这里只收 `publicKey`（base64）
// 或 `keyId`，并在响应里明确提示不要把私钥发到任何地方。

// trustStorePath 返回当前宿主的信任库路径。
func trustStorePath() string {
	return install.TrustStorePath(dataDirOrCwd())
}

// signatureModeOf 返回当前生效的签名策略（与安装器同一套默认值）。
func signatureModeOf() string {
	mode := conf.PluginSignatureMode()
	switch mode {
	case install.SignatureOff, install.SignatureWarn, install.SignatureRequire:
		return mode
	case "":
		// 与 install.Installer.signaturePolicy 保持一致：空值即 warn。
		// 两处默认值必须一样，否则界面显示的策略会和实际生效的不一致。
		return install.SignatureWarn
	default:
		return install.SignatureRequire
	}
}

// GetPluginTrust 返回签名策略与信任库内容。
func GetPluginTrust(c *gin.Context) {
	path := trustStorePath()
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		// 信任库文件坏了要如实报错：静默显示成"空库"会让管理员
		// 以为自己配的信任关系丢了，而实际是文件格式有问题。
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"mode":  signatureModeOf(),
			"path":  path,
			"keys":  []any{},
			"error": err.Error(),
		}})
		return
	}
	keys := make([]gin.H, 0, len(ring.Keys))
	for _, k := range ring.Keys {
		keys = append(keys, gin.H{
			"keyId":     k.KeyID,
			"name":      k.Name,
			"publicKey": k.PublicKey,
			"addedAt":   k.AddedAt,
		})
	}
	// 顺带统计已安装插件的签名情况，让界面一屏看清现状
	var total, signed, trusted int64
	if db.Get() != nil {
		db.Get().Model(&models.Plugin{}).Count(&total)
		db.Get().Model(&models.Plugin{}).Where("signature <> ''").Count(&signed)
		db.Get().Model(&models.Plugin{}).Where("trusted = ?", true).Count(&trusted)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"mode":           signatureModeOf(),
		"path":           path,
		"keys":           keys,
		"installedCount": total,
		"signedCount":    signed,
		"trustedCount":   trusted,
	}})
}

// AddPluginTrustKey 把一枚公钥加入信任库。
func AddPluginTrustKey(c *gin.Context) {
	var body struct {
		// PublicKey 是 base64 的 Ed25519 公钥（推荐）。
		PublicKey string `json:"publicKey"`
		// KeyID 只在发布者仅公布 keyId 时使用；安全性弱于给公钥。
		KeyID string `json:"keyId"`
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 明确挡住"把私钥贴进来"这件事：私钥是 64 字节 base64，
	// 与 32 字节的公钥长度不同，构造时会失败 —— 但错误信息要说清原因，
	// 否则用户只会看到"公钥长度异常"而不知道自己发了私钥。
	if looksLikePrivateKey(body.PublicKey) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"message": "这看起来是**私钥**（64 字节），不要把它发到任何地方。" +
				"请用 keygen 生成的公钥文件（*" + pbclus.PublicKeyFileSuffix + "）里的 publicKey 字段。",
		})
		return
	}

	var (
		rec pbclus.TrustedKey
		err error
	)
	switch {
	case strings.TrimSpace(body.PublicKey) != "":
		rec, err = pbclus.TrustRecordFromPublicKey(body.PublicKey, body.Name)
	case strings.TrimSpace(body.KeyID) != "":
		rec, err = pbclus.TrustRecordFromKeyID(body.KeyID, body.Name)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "需要 publicKey（base64 公钥）或 keyId",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	path := trustStorePath()
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		// 不静默重建：那会把管理员已有的信任关系一次抹掉
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"message": "现有信任库无法读取，已中止以免覆盖：" + err.Error(),
		})
		return
	}
	if existing, ok := ring.Lookup(rec.KeyID); ok && !body.Force {
		c.JSON(http.StatusConflict, gin.H{
			"code": 409,
			"message": "keyId " + rec.KeyID + " 已在信任库中（" + existing + "）。" +
				"要更新名字或公钥，请带 force=true 再调一次。",
			"existingName": existing,
		})
		return
	}
	rec.AddedAt = time.Now().UTC().Format(time.RFC3339)
	ring.Add(rec)
	if err := install.SaveTrustStore(path, ring); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "写入信任库失败: " + err.Error()})
		return
	}
	auditPlugin(c, "plugin_trust_add", rec.KeyID, "name="+rec.Name)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"keyId": rec.KeyID, "name": rec.Name, "count": len(ring.Keys),
		"warning": keyIDOnlyWarning(rec),
	}})
}

// keyIDOnlyWarning 在"只给了 keyId"时给出诚实的安全提示。
func keyIDOnlyWarning(rec pbclus.TrustedKey) string {
	if rec.PublicKey != "" {
		return ""
	}
	return "只登记了 keyId（没有公钥）。这样无法防止「同一个 keyId 配了别人的公钥」，" +
		"建议改用发布者提供的公钥。"
}

// looksLikePrivateKey 粗判一段 base64 是否是 64 字节的 Ed25519 私钥。
func looksLikePrivateKey(b64 string) bool {
	s := strings.TrimSpace(b64)
	if s == "" {
		return false
	}
	// 复用 pbclus 的解析：私钥 64 字节、公钥 32 字节，长度即可区分
	if rec, err := pbclus.TrustRecordFromPublicKey(s, ""); err == nil && rec.KeyID != "" {
		return false
	}
	// 解不出公钥时，再看它是不是合法长度（不是 base64 的本来就是坏输入，
	// 交给上面的分支去报错，这里只拦"明显是私钥"的情况）
	return base64DecodedLen(s) == 64
}

// base64DecodedLen 估算 base64 解码后的长度（不关心内容是否合法）。
func base64DecodedLen(s string) int {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if s == "" {
		return 0
	}
	for _, r := range s {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '+' || r == '/' || r == '-' || r == '_'
		if !ok {
			return -1
		}
	}
	return len(s) * 6 / 8
}

// RemovePluginTrustKey 从信任库移除一枚公钥。
//
// 这是**撤销**的唯一手段：私钥泄漏后，移除信任并重装插件。
// 注意已安装的插件不会被自动卸载或停用 —— 那个决定留给管理员，
// 但也因此要在响应里说清楚"已经装上的不会变"。
func RemovePluginTrustKey(c *gin.Context) {
	keyID := strings.TrimSpace(c.Param("keyId"))
	if keyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少 keyId"})
		return
	}
	path := trustStorePath()
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{
			"code": 409, "message": "信任库无法读取: " + err.Error(),
		})
		return
	}
	if !ring.Remove(keyID) {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404, "message": "信任库里没有 keyId " + keyID,
		})
		return
	}
	if err := install.SaveTrustStore(path, ring); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "写入信任库失败: " + err.Error()})
		return
	}
	auditPlugin(c, "plugin_trust_remove", keyID, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"keyId": keyID, "count": len(ring.Keys),
		"message": "已移除信任。已安装的插件不受影响；该密钥新签的包将不再被信任。",
	}})
}

// SetPluginTrustMode 修改签名策略。
func SetPluginTrustMode(c *gin.Context) {
	var body struct {
		Mode string `json:"mode"`
		// Confirm 在"有存量无签名插件"时是必需的。
		//
		// 必须有这个字段：设成 require 之后新安装都会被拒，
		// 这是会影响后续所有安装操作的改动，值得一次显式确认。
		// （早期版本在提示里写了"带 confirm=true"却没读这个字段，
		// 结果是该分支下 require 永远设不上 —— 提示与实现必须一致。）
		Confirm bool `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	switch mode {
	case install.SignatureOff, install.SignatureWarn, install.SignatureRequire:
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "mode 必须是 off / warn / require 之一",
		})
		return
	}
	// 设成 require 前说清后果：存量无签名插件会让新安装全部被拒
	if mode == install.SignatureRequire && !body.Confirm {
		var unsigned int64
		if db.Get() != nil {
			db.Get().Model(&models.Plugin{}).
				Where("signature = '' OR signature IS NULL").Count(&unsigned)
		}
		if unsigned > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"code": 409,
				"message": "有 " + strconv.FormatInt(unsigned, 10) + " 个已安装插件没有签名记录。" +
					"改成 require 之后**新安装**都必须受信任签名（已安装的不受影响）。" +
					"确认要继续就带 confirm=true 再调一次。",
				"needConfirm": true,
				"unsigned":    unsigned,
			})
			return
		}
	}
	if err := conf.SetPluginSignatureMode(mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": "保存配置失败: " + err.Error()})
		return
	}
	auditPlugin(c, "plugin_trust_mode", mode, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"mode":    mode,
		"message": signatureModeHint(mode),
	}})
}

func signatureModeHint(mode string) string {
	switch mode {
	case install.SignatureOff:
		return "已关闭签名检查。任何包都能安装，界面也不再提示签名状态。"
	case install.SignatureRequire:
		return "只接受受信任签名的包。没有信任任何发布者时，所有 PBClus 包都会被拒绝。"
	default:
		return "照常安装，但会提示签名状态（默认策略）。"
	}
}
