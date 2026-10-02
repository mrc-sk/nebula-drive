package jwt

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

var secret []byte

// Claims 自定义声明
type Claims struct {
	UserID    uint   `json:"uid"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// Init 从 settings 表加载或生成 JWT 密钥
func Init() error {
	if db.Get() == nil {
		return errors.New("db not ready")
	}
	var s models.Setting
	err := db.Get().Where("`key` = ?", "jwt_secret").First(&s).Error
	if err == nil {
		secret = []byte(s.Value)
		return nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	sec := base64.StdEncoding.EncodeToString(b)
	if err := db.Get().Create(&models.Setting{Key: "jwt_secret", Value: sec}).Error; err != nil {
		return err
	}
	secret = []byte(sec)
	return nil
}

// Sign 签发 token
func Sign(userID uint, sessionID string, ttl time.Duration) (string, error) {
	c := Claims{
		UserID:    userID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(secret)
}

// Parse 解析 token
func Parse(tok string) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(tok, c, func(t *jwt.Token) (interface{}, error) {
		// alg 混淆防御：显式限定 HMAC 族。
		// 攻击者可能把 alg 改成 none 或改成 RS256 并把公钥当 HMAC 密钥用。
		// 显式白名单是不依赖库的隐式行为做兜底 —— 换库版本也不会失守。
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
