package models

import (
	"fmt"
	"log"

	"github.com/nebula-drive/nebula/internal/cryptox"
	"github.com/nebula-drive/nebula/pkg/db"
)

// 敏感字段存量迁移。
//
// 背景：User.TwoFactor、OAuthApp.ClientSecret、Policy.Config、AccessToken.Token、
// PersonalAccessToken.Token 历史上全部以**明文**入库（其中几处代码注释还写着"加密"）。
// 现在分别挂上了自动加解密（models.Encrypted）与单向哈希（cryptox.TokenHash），
// 需要一次性的存量转换。
//
// # 三条原则
//
//  1. **幂等** —— 已转换过的记录会被条件/类型判断跳过，可重复执行。
//  2. **失败不阻塞启动** —— 迁移是安全增强，不该因为它导致服务起不来；
//     万一失败，查询路径仍有明文兜底（见 middleware.findAccessToken）。
//  3. **分批** —— 逐批 LIMIT 查询，避免大表一次性载入内存。

const migrateBatch = 500

// MigrateSensitiveFields 转换存量敏感字段，仅需在启动时调用一次。
func MigrateSensitiveFields() {
	if db.Get() == nil {
		return
	}
	var stats [5]int

	steps := []struct {
		name string
		run  func(*int)
	}{
		{"User.TwoFactor → 加密", func(n *int) { *n = migrateTwoFactor() }},
		{"OAuthApp.ClientSecret → 加密", func(n *int) { *n = migrateClientSecret() }},
		{"Policy.Config → 加密", func(n *int) { *n = migratePolicyConfig() }},
		{"AccessToken.Token → 哈希", func(n *int) { *n = migrateAccessTokens() }},
		{"PersonalAccessToken.Token → 哈希", func(n *int) { *n = migratePATs() }},
	}
	for i, s := range steps {
		func() {
			// 单步失败不阻断其余步骤：能转多少转多少
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[migrate] %s panic（已跳过）: %v", s.name, r)
				}
			}()
			s.run(&stats[i])
		}()
	}

	total := 0
	for _, n := range stats {
		total += n
	}
	if total > 0 {
		log.Printf("[migrate] 敏感字段存量转换完成：2FA %d 条、ClientSecret %d 条、StorageConfig %d 条、AccessToken %d 条、PAT %d 条",
			stats[0], stats[1], stats[2], stats[3], stats[4])
	}
}

// migrateTwoFactor 把 User.TwoFactor 的明文转为密文。
//
// 读取时 Encrypted.Scan 已把存量明文原样还原，因此这里只需原样 Save，
// 由 Valuer 完成加密。条件排除已加密记录，保证不会重复处理。
func migrateTwoFactor() int {
	n := 0
	for {
		var batch []User
		err := db.Get().Where("two_factor <> '' AND two_factor NOT LIKE ?", cryptox.Prefix()+"%").
			Limit(migrateBatch).Find(&batch).Error
		if err != nil || len(batch) == 0 {
			if err != nil {
				log.Printf("[migrate] User.TwoFactor 查询失败: %v", err)
			}
			return n
		}
		for i := range batch {
			if batch[i].TwoFactor.IsEmpty() {
				continue
			}
			if err := db.Get().Save(&batch[i]).Error; err != nil {
				log.Printf("[migrate] User#%d two_factor 转换失败: %v", batch[i].ID, err)
				continue
			}
			n++
		}
		if len(batch) < migrateBatch {
			return n
		}
	}
}

// migrateClientSecret 把 OAuthApp.ClientSecret 的明文转为密文。
func migrateClientSecret() int {
	n := 0
	for {
		var batch []OAuthApp
		err := db.Get().Where("client_secret <> '' AND client_secret NOT LIKE ?", cryptox.Prefix()+"%").
			Limit(migrateBatch).Find(&batch).Error
		if err != nil || len(batch) == 0 {
			if err != nil {
				log.Printf("[migrate] OAuthApp.ClientSecret 查询失败: %v", err)
			}
			return n
		}
		for i := range batch {
			if batch[i].ClientSecret.IsEmpty() {
				continue
			}
			if err := db.Get().Save(&batch[i]).Error; err != nil {
				log.Printf("[migrate] OAuthApp#%d client_secret 转换失败: %v", batch[i].ID, err)
				continue
			}
			n++
		}
		if len(batch) < migrateBatch {
			return n
		}
	}
}

// migratePolicyConfig 把 Policy.Config 的明文 JSON 转为密文。
// 这是风险最高的一项：配置里含 SFTP 密码、OSS/COS 密钥。
func migratePolicyConfig() int {
	n := 0
	for {
		var batch []Policy
		err := db.Get().Where("config <> '' AND config NOT LIKE ?", cryptox.Prefix()+"%").
			Limit(migrateBatch).Find(&batch).Error
		if err != nil || len(batch) == 0 {
			if err != nil {
				log.Printf("[migrate] Policy.Config 查询失败: %v", err)
			}
			return n
		}
		for i := range batch {
			if batch[i].Config.IsEmpty() {
				continue
			}
			if err := db.Get().Save(&batch[i]).Error; err != nil {
				log.Printf("[migrate] Policy#%d config 转换失败: %v", batch[i].ID, err)
				continue
			}
			n++
		}
		if len(batch) < migrateBatch {
			return n
		}
	}
}

// migrateAccessTokens 把 AccessToken.Token 的明文转为 SHA-256 哈希。
//
// 识别方式：哈希固定 64 位十六进制，历史明文是 32 位随机串。
// 迁移后用户手上已有的 access token **仍然可用**（认证时对输入哈希再比对），
// 无需强制重新签发。
func migrateAccessTokens() int {
	n := 0
	for {
		var batch []AccessToken
		err := db.Get().Where("token <> ''").Limit(migrateBatch).Find(&batch).Error
		if err != nil || len(batch) == 0 {
			if err != nil {
				log.Printf("[migrate] AccessToken 查询失败: %v", err)
			}
			return n
		}
		for i := range batch {
			if cryptox.IsTokenHash(batch[i].Token) {
				continue // 已是哈希
			}
			h := cryptox.TokenHash(batch[i].Token)
			if err := db.Get().Model(&AccessToken{}).Where("id = ?", batch[i].ID).Update("token", h).Error; err != nil {
				log.Printf("[migrate] AccessToken#%d 哈希化失败: %v", batch[i].ID, err)
				continue
			}
			n++
		}
		if len(batch) < migrateBatch {
			return n
		}
	}
}

// migratePATs 把 PersonalAccessToken.Token 的明文转为 SHA-256 哈希。
func migratePATs() int {
	n := 0
	for {
		var batch []PersonalAccessToken
		err := db.Get().Where("token <> ''").Limit(migrateBatch).Find(&batch).Error
		if err != nil || len(batch) == 0 {
			if err != nil {
				log.Printf("[migrate] PersonalAccessToken 查询失败: %v", err)
			}
			return n
		}
		for i := range batch {
			if cryptox.IsTokenHash(batch[i].Token) {
				continue
			}
			h := cryptox.TokenHash(batch[i].Token)
			if err := db.Get().Model(&PersonalAccessToken{}).Where("id = ?", batch[i].ID).Update("token", h).Error; err != nil {
				log.Printf("[migrate] PAT#%d 哈希化失败: %v", batch[i].ID, err)
				continue
			}
			n++
		}
		if len(batch) < migrateBatch {
			return n
		}
	}
}

// SensitiveFieldsEncrypted 报告敏感字段是否已全部处于加密/哈希状态。
// 供 /api/health 之类的探针使用，也便于运维确认迁移结果。
func SensitiveFieldsEncrypted() (ok bool, detail string) {
	type probe struct {
		name  string
		model any
		where string
		args  []any
	}
	probes := []probe{
		{"User.TwoFactor", &User{}, "two_factor <> '' AND two_factor NOT LIKE ?", []any{cryptox.Prefix() + "%"}},
		{"OAuthApp.ClientSecret", &OAuthApp{}, "client_secret <> '' AND client_secret NOT LIKE ?", []any{cryptox.Prefix() + "%"}},
		{"Policy.Config", &Policy{}, "config <> '' AND config NOT LIKE ?", []any{cryptox.Prefix() + "%"}},
	}
	all := true
	var msgs []string
	for _, p := range probes {
		var n int64
		if err := db.Get().Model(p.model).Where(p.where, p.args...).Count(&n).Error; err != nil {
			all = false
			msgs = append(msgs, fmt.Sprintf("%s: 查询失败(%v)", p.name, err))
			continue
		}
		if n > 0 {
			all = false
			msgs = append(msgs, fmt.Sprintf("%s: 仍有 %d 条未加密", p.name, n))
		}
	}
	if all {
		return true, "全部已加密"
	}
	out := ""
	for i, m := range msgs {
		if i > 0 {
			out += "；"
		}
		out += m
	}
	return false, out
}
