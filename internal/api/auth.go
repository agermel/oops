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
	if s.userStore == nil || s.tokenService == nil {
		http.Error(w, "auth not configured", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := s.userStore.Validate(username, password)
	if err != nil {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	token, err := s.tokenService.CreateToken(username, user.Name)
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
