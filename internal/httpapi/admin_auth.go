package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/adminauth"
)

type adminLoginBody struct {
	Password string `json:"password"`
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.admin == nil || !s.admin.Enabled() {
		writeAdminErr(w, http.StatusServiceUnavailable, "admin disabled; set ADMIN_PASSWORD")
		return
	}
	var body adminLoginBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeAdminErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	token, err := s.admin.Login(body.Password)
	if err != nil {
		if errors.Is(err, adminauth.ErrBadPassword) {
			writeAdminErr(w, http.StatusUnauthorized, "密码错误")
			return
		}
		writeAdminErr(w, http.StatusServiceUnavailable, "admin disabled; set ADMIN_PASSWORD")
		return
	}
	s.setAdminSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"expires_in": int(s.admin.TTL().Seconds()),
	})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.clearAdminSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminCheckAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	enabled := s.admin != nil && s.admin.Enabled()
	authed := enabled && s.admin.Validate(s.adminSessionToken(r)) == nil
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"enabled":       enabled,
		"authenticated": authed,
	})
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.admin == nil || !s.admin.Enabled() {
		writeAdminErr(w, http.StatusServiceUnavailable, "admin disabled; set ADMIN_PASSWORD")
		return false
	}
	if err := s.admin.Validate(s.adminSessionToken(r)); err != nil {
		writeAdminErr(w, http.StatusUnauthorized, "未登录或会话已过期")
		return false
	}
	return true
}

func (s *Server) adminSessionToken(r *http.Request) string {
	c, err := r.Cookie(adminauth.CookieName)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func (s *Server) setAdminSessionCookie(w http.ResponseWriter, token string) {
	ttl := 12 * time.Hour
	if s.admin != nil {
		ttl = s.admin.TTL()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminauth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Expires:  s.now().Add(ttl),
	})
}

func (s *Server) clearAdminSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminauth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func writeAdminErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"ok": false,
		"error": map[string]any{
			"message": message,
		},
	})
}
