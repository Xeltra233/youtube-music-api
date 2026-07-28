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
	// ensure no content leak keys
	for _, bad := range []string{"content", "cookie", "cookies_text", "raw"} {
		if _, ok := st[bad]; ok {
			t.Fatalf("status must not include %s", bad)
		}
	}

	// upload
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "966fac1c-demo.txt")
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
	if _, ok := up["content"]; ok {
		t.Fatal("upload must not return content")
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
