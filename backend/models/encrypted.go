package models

import (
	"database/sql/driver"
	"fmt"
	"log"

	"github.com/nebula-drive/nebula/internal/cryptox"
)

// Encrypted 是数据库中「自动加解密」的字符串字段类型。
//
// 用法：把敏感字段的类型从 string 改成 Encrypted，赋值/读取的业务代码基本不变，
// 数据库里存的是密文。加密在写库时（driver.Valuer）完成，解密在读取时
// （sql.Scanner）完成，业务层拿到的永远是明文。
//
// 为什么不用 GORM 的 BeforeSave/AfterFind 钩子：钩子只对 struct 字段生效，
// 覆盖不到 `db.Model(&u).Update("two_factor", v)` 这类值来自 map 的写法。
// 而 database/sql 的 Valuer/Scanner 位于所有写入路径的必经之处。
type Encrypted string

// Value 写库时调用：把明文加密为密文。
//
// 加密失败必须返回 error 让整次写入失败（fail closed）——
// 静默回落成明文等于把配置故障变成数据泄露。
func (e Encrypted) Value() (driver.Value, error) {
	enc, err := cryptox.Encrypt(string(e))
	if err != nil {
		return nil, fmt.Errorf("cryptox encrypt: %w", err)
	}
	return enc, nil
}

// Scan 读库时调用：把密文还原为明文。
//
// 存量数据没有加密前缀，会被 cryptox.Decrypt 当作明文原样返回 —— 这正是
// 平滑升级的关键：老数据照常可读。
//
// 解密失败（例如 data/secret.key 丢失或被更换）时**保留原始值并放行读取**：
// 读失败就返回 error 会让整条记录不可用，用户连账号都登不进；这种情况
// 更糟。宁可让用户看到一个用不了的密文，也不要让系统整体瘫掉。日志会明确告警。
func (e *Encrypted) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		*e = ""
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("models: cannot scan %T into Encrypted", src)
	}
	dec, err := cryptox.Decrypt(s)
	if err != nil {
		log.Printf("[models] Encrypted.Scan: 解密失败，保留原值（密钥是否被更换/丢失？）: %v", err)
		*e = Encrypted(s)
		return nil
	}
	*e = Encrypted(dec)
	return nil
}

// IsEmpty 字段是否为空。用于替代原先的 `field == ""` 判断
// （Encrypted 与 untyped string 常量不能直接比较）。
func (e Encrypted) IsEmpty() bool { return e == "" }

// String 取明文值。需要把敏感字段传给库外函数（校验、解析）时使用。
func (e Encrypted) String() string { return string(e) }

// From 从普通 string 构造 Encrypted。
func From(s string) Encrypted { return Encrypted(s) }
