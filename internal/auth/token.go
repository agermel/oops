package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是 JWT 中携带的用户信息。
type Claims struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	jwt.RegisteredClaims
}

// TokenService 签发和验证 JWT。
type TokenService struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenService 创建 TokenService。密钥从用户的密码 hash 派生。
func NewTokenService(passwordHash string, ttl time.Duration) *TokenService {
	h := sha256.Sum256([]byte(passwordHash))
	return &TokenService{secret: h[:], ttl: ttl}
}

// NewTokenServiceRandom 创建使用随机密钥的 TokenService。
// 用于用户尚未设置时的过渡阶段——setup 完成后会用真实密钥重建。
func NewTokenServiceRandom(ttl time.Duration) (*TokenService, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate random secret: %w", err)
	}
	return &TokenService{secret: secret, ttl: ttl}, nil
}

// CreateToken 为用户签发 JWT。
func (ts *TokenService) CreateToken(username, name string) (string, error) {
	now := time.Now()
	claims := &Claims{
		Username: username,
		Name:     name,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(now),
			ExpiresAt: func() *jwt.NumericDate {
				if ts.ttl > 0 {
					return jwt.NewNumericDate(now.Add(ts.ttl))
				}
				return nil
			}(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(ts.secret)
}

// VerifyToken 验证 JWT 并返回 claims。
func (ts *TokenService) VerifyToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return ts.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
