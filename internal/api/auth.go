package api

import (
	"net/http"
	"strings"
	"time"
)

// isHTTPS 检测请求是否通过 HTTPS 到达（直接 TLS 或反代）。
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// handleCreateToken 处理 POST /api/token，验证凭证并签发 JWT Cookie。
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.UserStore == nil || s.TokenService == nil {
		http.Error(w, "auth not configured", http.StatusInternalServerError)
		return
	}

	// FormData (multipart) 和 urlencoded 都兼容
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	// 登录限流：按 username+ip 限制失败次数，防暴力破解。
	limitKey := loginLimitKey(username, r)
	if !defaultLoginLimiter.allow(limitKey) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many login attempts, please try again later", http.StatusTooManyRequests)
		return
	}

	user, err := s.UserStore.Validate(username, password)
	if err != nil {
		defaultLoginLimiter.recordFail(limitKey)
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	defaultLoginLimiter.recordSuccess(limitKey)

	token, err := s.TokenService.CreateToken(username, user.Name)
	if err != nil {
		http.Error(w, "token creation failed", http.StatusInternalServerError)
		return
	}

	var expires time.Time
	if s.tokenTTL > 0 {
		expires = time.Now().Add(s.tokenTTL)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "jwt",
		Value:    token,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		Expires:  expires,
	})
	w.WriteHeader(http.StatusOK)
}

// handleDeleteToken 处理 DELETE /api/token，清除 JWT Cookie。
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jwt",
		Value:    "",
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	w.WriteHeader(http.StatusOK)
}

// handleAuthMe 返回当前登录用户信息。
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]string{
		"name": user.Name,
	})
}
