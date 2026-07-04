package api

import (
	"net/http"
	"strings"
	"time"

	"oops/internal/auth"
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
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.UserStore == nil || !s.UserStore.IsSetup() || s.TokenService == nil {
		writeJSONError(w, "auth not configured", http.StatusInternalServerError)
		return
	}

	// FormData (multipart) 和 urlencoded 都兼容
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeJSONError(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	// 登录限流：按 username+ip 限制失败次数，防暴力破解。
	limitKey := loginLimitKey(username, r)
	if !defaultLoginLimiter.allow(limitKey) {
		w.Header().Set("Retry-After", "900")
		writeJSONError(w, "too many login attempts, please try again later", http.StatusTooManyRequests)
		return
	}

	user, err := s.UserStore.Validate(username, password)
	if err != nil {
		defaultLoginLimiter.recordFail(limitKey)
		writeJSONError(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	defaultLoginLimiter.recordSuccess(limitKey)

	token, err := s.TokenService.CreateToken(username, user.Name)
	if err != nil {
		writeJSONError(w, "token creation failed", http.StatusInternalServerError)
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
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
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

// handleAuthStatus 返回系统认证状态（无需登录即可访问）。
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	setup := s.UserStore != nil && s.UserStore.IsSetup()
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]any{
		"setup": setup,
	})
}

// handleSetup 处理 POST /api/setup，创建初始管理员账户。
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.UserStore == nil {
		writeJSONError(w, "auth store not available", http.StatusInternalServerError)
		return
	}
	if s.UserStore.IsSetup() {
		writeJSONError(w, "user already configured", http.StatusConflict)
		return
	}

	// 兼容 multipart 和 urlencoded
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeJSONError(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	username := r.FormValue("username")
	name := r.FormValue("name")
	password := r.FormValue("password")

	if err := s.UserStore.Setup(username, name, password); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 用新用户的密码哈希重建 TokenService，使已有 JWT 失效
	s.TokenService = auth.NewTokenService(s.UserStore.User.Password, s.tokenTTL)

	// 签发 JWT Cookie，同登录流程
	token, err := s.TokenService.CreateToken(username, name)
	if err != nil {
		writeJSONError(w, "token creation failed", http.StatusInternalServerError)
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
