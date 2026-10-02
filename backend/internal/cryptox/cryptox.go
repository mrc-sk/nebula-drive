// Package cryptox 提供敏感字段的透明加解密。
//
// 密钥复用 conf 包的 data/secret.key（AES-256-GCM），不额外引入密钥管理。
//
// # 密文带版本前缀
//
// 加密结果形如 "enc:v1:<base64>"。前缀的作用是**区分密文与存量明文**：
// 这让"平滑升级"成为可能——存量未加密的数据读出来仍然是原文，
// 可以在后续写入时顺带升级为密文，而不需要停机做一次性全量迁移。
//
// # 为什么不用 GORM hook
//
// 钩子（BeforeSave/AfterFind）只对 struct 字段生效，无法覆盖
//   db.Model(&u).Update("two_factor", secret)   // 值来自 map，不经过 struct
// 这类写法。项目里 2FA 开关与重置恰好就是这种写法，所以本包配合
// models.Encrypted 类型的 driver.Valuer / sql.Scanner 实现，
// 覆盖**所有**数据库写入路径。
package cryptox

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/nebula-drive/nebula/conf"
)

// prefix 密文前缀，带版本号以便将来轮换算法时区分。
const prefix = "enc:v1:"

// IsEncrypted 判断 s 是否为本包生成的密文。
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, prefix)
}

// Prefix 返回密文前缀。供迁移脚本构造 SQL 条件（LIKE 'enc:v1:%'），
// 避免在前缀字面量上出现第二处需要同步的硬编码。
func Prefix() string { return prefix }

// Encrypt 把明文加密为带前缀的密文。
//
// 幂等：已是密文或为空则原样返回。
// 失败返回 error —— 调用方**必须**让这次数据库写入失败。
// 绝不能在这里静默返回明文，那等于把"加密失败"变成"静默泄露"。
func Encrypt(s string) (string, error) {
	if s == "" || IsEncrypted(s) {
		return s, nil
	}
	enc, err := conf.EncryptString(s)
	if err != nil {
		return "", err
	}
	return prefix + enc, nil
}

// Decrypt 解密带前缀的密文。
//
// 无前缀的输入按**明文**原样返回 —— 这是兼容存量未加密数据的关键。
// 返回 error 表示密钥不匹配或密文损坏，此时 s 原样返回，由调用方决定如何处理。
func Decrypt(s string) (string, error) {
	if s == "" || !IsEncrypted(s) {
		return s, nil
	}
	return conf.DecryptString(strings.TrimPrefix(s, prefix))
}

// TokenHash 计算访问令牌的存储形式（SHA-256，64 位十六进制）。
//
// # 为什么令牌用哈希而不是加密
//
// 访问令牌（OAuth2 access token / Personal Access Token）与 TOTP secret、
// 存储密码是**不同性质**的字段：令牌永远不需要读回原值——创建时展示一次给用户，
// 之后所有场景都只是比对。因此可逆加密在这里是错的：
//
//   - AES-GCM 带随机 nonce，每次加密结果都不同 → 无法用 WHERE token = ? 查询
//   - 两列相同明文会得到不同密文 → 唯一索引失效
//
// 单向哈希同时解决了这两点，而且安全性更高：数据库泄露也无法直接冒用令牌。
//
// # 与存量明文共存
//
// 哈希值固定 64 位十六进制；历史明文令牌是 32/36 位随机串。
// 迁移脚本据此识别（见 models.MigrateSensitiveFields），
// 查询路径也保留明文兜底（见 middleware.APIAuth）。
func TokenHash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// IsTokenHash 判断 s 是否为 TokenHash 的输出（64 位十六进制）。
func IsTokenHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
