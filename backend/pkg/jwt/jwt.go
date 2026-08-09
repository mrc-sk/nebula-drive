package jwt

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
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
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
