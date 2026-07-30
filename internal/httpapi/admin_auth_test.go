package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/adminauth"
)

func TestAdminLoginLogoutCheckAuth(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm-pass"
	cfg.AdminSessionSecret = "adm-secret"
	cfg.AdminSessionTTL = time.Hour
	cfg.APIKey = "bot-key" // admin paths must bypass API key

	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	// check-auth without login
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/check-auth", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("check-auth status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["authenticated"] != false || st["enabled"] != true {
		t.Fatalf("unexpected check-auth: %v", st)
	}

	// wrong password
	rr = httptest.NewRecorder()
	body := bytes.NewBufferString(`{"password":"nope"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login", body)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status=%d body=%s", rr.Code, rr.Body.String())
	}

	// good login
	rr = httptest.NewRecorder()
	body = bytes.NewBufferString(`{"password":"adm-pass"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login", body)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Header.Get("Set-Cookie")
	if !strings.Contains(cookie, adminauth.CookieName+"=") {
		t.Fatalf("missing session cookie: %s", cookie)
	}

	// check-auth with cookie (no API key)
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/check-auth", nil)
	req2.Header.Set("Cookie", cookieHeaderFromSetCookie(cookie))
	h.ServeHTTP(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("authed check status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	st = map[string]any{}
	_ = json.Unmarshal(rr2.Body.Bytes(), &st)
	if st["authenticated"] != true {
		t.Fatalf("want authenticated true: %v", st)
	}

	// logout
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	req3.Header.Set("Cookie", cookieHeaderFromSetCookie(cookie))
	h.ServeHTTP(rr3, req3)
	if rr3.Code != 200 {
		t.Fatalf("logout status=%d", rr3.Code)
	}
	clearCookie := rr3.Result().Header.Get("Set-Cookie")
	if !strings.Contains(clearCookie, "Max-Age=0") && !strings.Contains(strings.ToLower(clearCookie), "max-age=-1") {
		// Accept Max-Age=-1 or expires in past
		if !strings.Contains(strings.ToLower(clearCookie), "expires=") {
			t.Fatalf("logout should clear cookie: %s", clearCookie)
		}
	}
}

func TestAdminDisabled(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = ""
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewBufferString(`{"password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminSessionCookieSecureForHTTPS(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm-pass"
	cfg.AdminSessionSecret = "adm-secret"
	cfg.AdminSessionTTL = time.Hour
	h := newTestServer(t, cfg, nil, nil).Handler()

	req := httptest.NewRequest(http.MethodPost, "https://music.example.com/api/admin/login", bytes.NewBufferString(`{"password":"adm-pass"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	setCookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "; Secure") || !strings.Contains(setCookie, "; HttpOnly") {
		t.Fatalf("HTTPS admin cookie flags=%q", setCookie)
	}
}

func cookieHeaderFromSetCookie(setCookie string) string {
	// take first segment name=value
	part := strings.Split(setCookie, ";")[0]
	return strings.TrimSpace(part)
}
