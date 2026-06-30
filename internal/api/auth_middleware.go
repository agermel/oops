package api

import (
	"context"
	"net/http"

	"oops/internal/auth"
)

type contextKey string

const userContextKey contextKey = "user"

// authMiddleware 从 jwt Cookie 提取 JWT，验证后注入 context。
// 不阻断请求 —— 只填充 context。requireAuth 负责阻断。
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.TokenService != nil {
			if cookie, err := r.Cookie("jwt"); err == nil {
				if claims, err := s.TokenService.VerifyToken(cookie.Value); err == nil {
					user := &auth.User{Name: claims.Name, Username: claims.Username}  // 注意：store.User.Username 实际是用户名，Name 是显示名
					ctx := context.WithValue(r.Context(), userContextKey, user)
					r = r.WithContext(ctx)
				}
			}
		}
		next(w, r)
	}
}

// requireAuth 检查请求是否已通过鉴权（Cookie JWT）。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); ok {
			next(w, r)
			return
		}
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
	}
}

// UserFromContext 从 context 中提取已认证用户。
func UserFromContext(ctx context.Context) (*auth.User, bool) {
	u, ok := ctx.Value(userContextKey).(*auth.User)
	return u, ok && u != nil
}
