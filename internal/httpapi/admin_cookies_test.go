package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

func TestAdminCookieUploadAndStatus(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm"
	cfg.AdminSessionSecret = "sec"
	cfg.AdminSessionTTL = time.Hour
	cfg.CookiesDir = filepath.Join(cfg.DownloadDir, "cookies-data")
	cfg.CookiesFile = filepath.Join(cfg.CookiesDir, cookies.StableFileName)
	cfg.APIKey = "bot-key"

	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	// login
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"password":"adm"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	cookie := cookieHeaderFromSetCookie(rr.Result().Header.Get("Set-Cookie"))

	// unauthorized status without cookie
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/cookies/status", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth=%d", rr.Code)
	}

	// empty status
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/cookies/status", nil)
	req.Header.Set("Cookie", cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var st map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &st)
	if st["present"] != false {
		t.Fatalf("want present=false: %v", st)
	}
	if st["source"] != cookies.CookieSourceNone || st["logged_in"] != false ||
		st["last_sync_result"] != cookies.CookieSyncResultNever || st["last_sync_error"] != "" {
		t.Fatalf("unexpected empty cookie status: %v", st)
	}
	// ensure no content leak keys
	for _, bad := range []string{"content", "cookie", "cookies_text", "raw", "cookies_dir"} {
		if _, ok := st[bad]; ok {
			t.Fatalf("status must not include %s", bad)
		}
	}

	// upload
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", cookies.StableFileName)
	if err != nil {
		t.Fatal(err)
	}
	sample := "# Netscape HTTP Cookie File\n\n.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\ttestlogin\n.youtube.com\tTRUE\t/\tTRUE\t0\t__Secure-3PSID\tsidvalue\n"
	if _, err := part.Write([]byte(sample)); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/cookies/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("upload %d %s", rr.Code, rr.Body.String())
	}
	var up map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &up)
	if up["present"] != true {
		t.Fatalf("upload response present: %v", up)
	}
	if up["source"] != cookies.CookieSourceFile || up["logged_in"] != true || up["valid"] != true {
		t.Fatalf("upload quality/source metadata: %v", up)
	}
	if up["last_sync_result"] != cookies.CookieSyncResultNever {
		t.Fatalf("file upload must not fabricate browser sync: %v", up)
	}
	if up["uploaded_as"] == cookies.StableFileName {
		t.Fatalf("stable filename upload must be staged as a drop-in: %v", up)
	}
	if _, ok := up["content"]; ok {
		t.Fatal("upload must not return content")
	}
	for _, secret := range []string{"testlogin", "sidvalue"} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Fatalf("upload response leaked cookie value %q", secret)
		}
	}

	// stable file exists on disk
	stable := filepath.Join(cfg.CookiesDir, cookies.StableFileName)
	if !cookies.FileExistsNonEmpty(stable) {
		// original drop-in should exist at least
		entries, _ := os.ReadDir(cfg.CookiesDir)
		if len(entries) == 0 {
			t.Fatal("no files written")
		}
	}

	// status present
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/cookies/status", nil)
	req.Header.Set("Cookie", cookie)
	h.ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &st)
	if st["present"] != true {
		t.Fatalf("status after upload: %v", st)
	}
	if st["source"] != cookies.CookieSourceFile || st["logged_in"] != true {
		t.Fatalf("status source/login after upload: %v", st)
	}
}

func TestAdminCookieUploadRejectsNonText(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm"
	cfg.CookiesDir = filepath.Join(cfg.DownloadDir, "cdir")
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"password":"adm"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	cookie := cookieHeaderFromSetCookie(rr.Result().Header.Get("Set-Cookie"))

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", "x.bin")
	_, _ = part.Write([]byte{0x00, 0x01, 0x02, 0xff})
	_ = w.Close()

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/cookies/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", cookie)
	h.ServeHTTP(rr, req)
	if rr.Code == 200 {
		t.Fatalf("binary upload should fail, body=%s", rr.Body.String())
	}
}

func TestAdminCookieUploadStaysFallbackWhileManagedSourceIsActive(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm"
	cfg.AdminSessionSecret = "sec"
	cfg.AdminSessionTTL = time.Hour
	cfg.CookiesDir = filepath.Join(cfg.DownloadDir, "managed-fallback")
	cfg.CookiesFile = filepath.Join(cfg.CookiesDir, cookies.StableFileName)
	if err := os.MkdirAll(cfg.CookiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	managedJar := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tmanaged-login\n.google.com\tTRUE\t/\tTRUE\t0\tSID\tmanaged-sid\n.google.com\tTRUE\t/\tTRUE\t0\tSAPISID\tmanaged-api\n"
	if err := os.WriteFile(cfg.CookiesFile, []byte(managedJar), 0o600); err != nil {
		t.Fatal(err)
	}
	source := cookies.NewSourceArbiter(cookies.CookieSourceModeAuto, false)
	source.SetManagedAuthenticated(true)
	srv := newTestServer(t, cfg, nil, nil)
	srv.cookieSource = source
	h := srv.Handler()

	login := doJSON(t, h, http.MethodPost, "/api/admin/login", map[string]any{"password": "adm"}, nil)
	cookie := cookieHeaderFromSetCookie(login.Header().Get("Set-Cookie"))
	fallbackJar := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tfallback-login\n.google.com\tTRUE\t/\tTRUE\t0\tSID\tfallback-sid\n.google.com\tTRUE\t/\tTRUE\t0\tSAPISID\tfallback-api\n"
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fallback.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(fallbackJar))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cookies/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	afterUpload, err := os.ReadFile(cfg.CookiesFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterUpload) != managedJar {
		t.Fatal("fallback upload replaced the active managed jar")
	}
	var payload map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	if payload["source"] != cookies.CookieSourceManaged || payload["managed_authenticated"] != true {
		t.Fatalf("managed status=%v", payload)
	}

	source.SetManagedAuthenticated(false)
	status := doJSON(t, h, http.MethodGet, "/api/admin/cookies/status", nil, map[string]string{"Cookie": cookie})
	if status.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	afterFallback, err := os.ReadFile(cfg.CookiesFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFallback) != fallbackJar {
		t.Fatal("stored fallback was not promoted after managed source ended")
	}
}

type cookieSyncStatusStub struct {
	status cookies.CookieSyncStatus
	calls  int
}

func (s *cookieSyncStatusStub) CookieSyncStatus() cookies.CookieSyncStatus {
	s.calls++
	return s.status
}

func TestAdminCookieStatusBrowserMetadataIsAuthenticatedAndSanitized(t *testing.T) {
	cfg := testCfg(t)
	cfg.AdminPassword = "adm"
	cfg.AdminSessionSecret = "sec"
	cfg.AdminSessionTTL = time.Hour
	cfg.CookiesDir = filepath.Join(cfg.DownloadDir, "browser-status")
	cfg.CookiesFile = filepath.Join(cfg.CookiesDir, cookies.StableFileName)
	cfg.CookiesFromBrowser = `chrome:C:\Users\SECRET_PROFILE\Default`
	cfg.Proxy = "http://user:SECRET_PROXY@127.0.0.1:7890"
	if err := os.MkdirAll(cfg.CookiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jar := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tCOOKIE_SECRET_LOGIN\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSID\tCOOKIE_SECRET_SID\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSAPISID\tCOOKIE_SECRET_SAPISID\n"
	if err := os.WriteFile(cfg.CookiesFile, []byte(jar), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 7, 30, 12, 34, 56, 0, time.UTC)
	provider := &cookieSyncStatusStub{status: cookies.CookieSyncStatus{
		BrowserConfigured: true,
		LastPhase:         cookies.CookieSyncPhasePeriodic,
		LastResult:        cookies.CookieSyncResultFailed,
		LastError:         "RAW_SECRET_PROFILE_ERROR",
		LastUpdated:       true,
		LastSyncAt:        fixed,
		LastSuccessAt:     fixed.Add(-time.Hour),
	}}
	base := newTestServer(t, cfg, nil, nil)
	srv, err := New(Options{
		Config:           cfg,
		Searcher:         base.searcher,
		Sessions:         base.sessions,
		Downloader:       base.downloader,
		CookieSyncStatus: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	// Authentication is checked before the provider or filesystem status work.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/cookies/status", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized || provider.calls != 0 {
		t.Fatalf("unauthorized status code=%d provider_calls=%d", rr.Code, provider.calls)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"password":"adm"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	adminCookie := cookieHeaderFromSetCookie(rr.Result().Header.Get("Set-Cookie"))

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/cookies/status", nil)
	req.Header.Set("Cookie", adminCookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls=%d want 1", provider.calls)
	}
	var status map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["source"] != cookies.CookieSourceBrowser || status["browser_configured"] != true ||
		status["logged_in"] != true || status["valid"] != true {
		t.Fatalf("browser/file quality status mismatch: %v", status)
	}
	if status["last_sync_phase"] != cookies.CookieSyncPhasePeriodic ||
		status["last_sync_result"] != cookies.CookieSyncResultFailed ||
		status["last_sync_error"] != cookies.CookieSyncErrorGeneric ||
		status["last_sync_updated"] != false {
		t.Fatalf("bounded sync status mismatch: %v", status)
	}
	if status["last_sync_at"] != fixed.Format(time.RFC3339) ||
		status["last_success_at"] != fixed.Add(-time.Hour).Format(time.RFC3339) {
		t.Fatalf("sync timestamps mismatch: %v", status)
	}
	response := rr.Body.String()
	for _, secret := range []string{
		"SECRET_PROFILE",
		"SECRET_PROXY",
		"COOKIE_SECRET_LOGIN",
		"COOKIE_SECRET_SID",
		"COOKIE_SECRET_SAPISID",
		"RAW_SECRET_PROFILE_ERROR",
		cfg.CookiesDir,
		cfg.CookiesFile,
	} {
		if strings.Contains(response, secret) {
			t.Fatalf("status response leaked %q: %s", secret, response)
		}
	}
	for _, forbiddenKey := range []string{"browser_spec", "proxy", "content", "raw", "cookies_text", "cookies_dir"} {
		if _, ok := status[forbiddenKey]; ok {
			t.Fatalf("status response included forbidden key %q", forbiddenKey)
		}
	}
}
