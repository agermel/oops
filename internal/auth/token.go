package auth

import (
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

// NewTokenService 创建 TokenService。密钥从所有用户的密码 hash 派生（照抄 Dozzle）。
func NewTokenService(users map[string]*User, ttl time.Duration) *TokenService {
	h := sha256.New()
	for _, u := range users {
		h.Write([]byte(u.Password))
	}
	return &TokenService{secret: h.Sum(nil), ttl: ttl}
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
