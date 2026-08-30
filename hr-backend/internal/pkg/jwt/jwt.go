// Package jwt 封装 JWT v5 的签发与解析。
//
// claims 承载 account_id、username 与过期时间（exp），用 HS256 对称签名。
// 密钥与 TTL 由 config.JWT 提供。
package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// issuer 是 token 的签发方标识，解析时强制校验，防止单服务密钥被复用时跨服务重放。
const issuer = "sili-smart-hr"

// Claims 是写入 token 的声明。
type Claims struct {
	AccountID int64  `json:"account_id"`
	Username  string `json:"username"`
	jwt.RegisteredClaims
}

// Manager 负责签发与解析 token。
type Manager struct {
	secret []byte
	ttl    time.Duration
}

// NewManager 构造一个 Manager。
func NewManager(secret string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl}
}

// Generate 为指定账号签发 token。
func (m *Manager) Generate(accountID int64, username string) (string, error) {
	if len(m.secret) == 0 {
		return "", errors.New("jwt: secret is empty")
	}
	now := time.Now()
	claims := Claims{
		AccountID: accountID,
		Username:  username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			Subject:   username,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// Parse 解析并校验 token，返回声明。
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("jwt: unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(issuer))
	if err != nil {
		return nil, err
	}
	return &claims, nil
}
