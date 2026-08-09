package service

import (
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/util"
	"github.com/pquerna/otp/totp"
)

// ValidateTOTP 校验两步验证码
func ValidateTOTP(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}

// NewTOTPSecret 生成 2FA 密钥
func NewTOTPSecret(account string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "NebulaDrive", AccountName: account})
	if err != nil {
		return "", err
	}
	return key.Secret(), nil
}

// GenEmailCode 生成邮箱验证码（15 分钟有效）
func GenEmailCode(email, purpose string) (string, error) {
	code := util.RandomCode(6)
	db.Get().Where("email = ? AND purpose = ?", email, purpose).Delete(&models.EmailCode{})
	err := db.Get().Create(&models.EmailCode{
		Email:     email,
		Code:      code,
		Purpose:   purpose,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}).Error
	return code, err
}

// VerifyEmailCode 校验并消耗验证码
func VerifyEmailCode(email, purpose, code string) bool {
	var ec models.EmailCode
	err := db.Get().Where("email = ? AND purpose = ? AND used = ? AND expires_at > ?",
		email, purpose, false, time.Now()).First(&ec).Error
	if err != nil || ec.Code != code {
		return false
	}
	db.Get().Model(&ec).Update("used", true)
	return true
}
