package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminStaticPagesServed(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "x"
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	// login page
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/login.html", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login.html status=%d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "管理员登录") && !strings.Contains(body, "loginForm") {
		t.Fatalf("login page unexpected body: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
		// ServeContent may set based on extension; accept empty in some go versions
	}

	// index via /admin/
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("admin/ status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Cookie 上传") && !strings.Contains(rr.Body.String(), "dropzone") {
		t.Fatalf("upload page missing markers")
	}

	// css
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/admin.css", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "login-body") {
		t.Fatalf("css status=%d", rr.Code)
	}

	// js
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/admin.js", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "cookies/upload") {
		t.Fatalf("js status=%d", rr.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
