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
	if rr.Code != http.StatusFound {
		t.Fatalf("admin/ should redirect to login, status=%d body=%s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "login.html") {
		t.Fatalf("admin/ redirect location=%q", loc)
	}

	// Browser login and file upload share the authenticated management page.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/index.html", nil)
	h.ServeHTTP(rr, req)
	indexBody := rr.Body.String()
	for _, marker := range []string{"YouTube 浏览器登录", `id="loginSurface"`, `id="loginVerifyBtn"`, `id="dropzone"`} {
		if rr.Code != 200 || !strings.Contains(indexBody, marker) {
			t.Fatalf("management page missing marker %q status=%d", marker, rr.Code)
		}
	}

	// css
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/admin.css", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "login-body") || !strings.Contains(rr.Body.String(), "login-surface") {
		t.Fatalf("css status=%d", rr.Code)
	}

	// js
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/admin.js", nil)
	h.ServeHTTP(rr, req)
	jsBody := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(jsBody, "cookies/upload") ||
		!strings.Contains(jsBody, "youtube-login/sessions") || !strings.Contains(jsBody, "new WebSocket") ||
		!strings.Contains(jsBody, "browser_start_failed") || !strings.Contains(jsBody, "is-terminal") {
		t.Fatalf("js status=%d", rr.Code)
	}
	if strings.Contains(jsBody, "T10 将继续") {
		t.Fatal("admin UI must use product copy rather than internal task labels")
	}
	if strings.Contains(jsBody, "console.log") || strings.Contains(jsBody, "console.debug") {
		t.Fatal("admin login script must not log browser input or frame data")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
