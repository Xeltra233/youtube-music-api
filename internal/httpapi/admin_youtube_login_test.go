package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xeltra/ytmusic-bridge/internal/adminauth"
	"github.com/xeltra/ytmusic-bridge/internal/cookies"
	"github.com/xeltra/ytmusic-bridge/internal/managedlogin"
)

type httpLoginLauncher struct {
	browser *httpLoginBrowser
	err     error
}

type lockedLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (l *httpLoginLauncher) Launch(context.Context, managedlogin.LaunchOptions) (managedlogin.Browser, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.browser, nil
}

type httpLoginBrowser struct {
	mu         sync.Mutex
	frames     chan managedlogin.Frame
	done       chan error
	closeOnce  sync.Once
	viewport   managedlogin.Viewport
	cookies    []managedlogin.BrowserCookie
	exportErr  error
	inputs     []managedlogin.InputEvent
	clearCalls int
}

func newHTTPLoginBrowser(browserCookies []managedlogin.BrowserCookie) *httpLoginBrowser {
	return &httpLoginBrowser{
		frames:   make(chan managedlogin.Frame, 1),
		done:     make(chan error, 1),
		viewport: managedlogin.Viewport{Width: 1280, Height: 800},
		cookies:  append([]managedlogin.BrowserCookie(nil), browserCookies...),
	}
}

func (b *httpLoginBrowser) Frames() <-chan managedlogin.Frame { return b.frames }
func (b *httpLoginBrowser) Done() <-chan error                { return b.done }
func (b *httpLoginBrowser) Viewport() managedlogin.Viewport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.viewport
}
func (b *httpLoginBrowser) Dispatch(_ context.Context, event managedlogin.InputEvent) error {
	b.mu.Lock()
	b.inputs = append(b.inputs, event)
	b.mu.Unlock()
	return nil
}
func (b *httpLoginBrowser) Resize(_ context.Context, viewport managedlogin.Viewport) error {
	b.mu.Lock()
	b.viewport = viewport
	b.mu.Unlock()
	return nil
}
func (b *httpLoginBrowser) ExportCookies(context.Context) ([]managedlogin.BrowserCookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exportErr != nil {
		return nil, b.exportErr
	}
	return append([]managedlogin.BrowserCookie(nil), b.cookies...), nil
}
func (b *httpLoginBrowser) ClearCookies(context.Context) error {
	b.mu.Lock()
	b.clearCalls++
	b.cookies = nil
	b.mu.Unlock()
	return nil
}
func (b *httpLoginBrowser) Close(context.Context) error {
	b.closeOnce.Do(func() {
		b.done <- nil
		close(b.done)
	})
	return nil
}

func TestManagedLoginRESTAuthIsolationMutualExclusionAndVerify(t *testing.T) {
	browser := newHTTPLoginBrowser(httpAuthenticatedCookies())
	srv, manager, admin := newManagedLoginHTTPServer(t, browser, log.Printf)
	h := srv.Handler()
	tokenA, _ := admin.Login("adm-pass")
	tokenB, _ := admin.Login("adm-pass")
	cookieA := adminauth.CookieName + "=" + tokenA
	cookieB := adminauth.CookieName + "=" + tokenB

	unauth := doJSON(t, h, http.MethodPost, "/api/admin/youtube-login/sessions", nil, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d body=%s", unauth.Code, unauth.Body.String())
	}

	created := doJSON(t, h, http.MethodPost, "/api/admin/youtube-login/sessions", nil, map[string]string{"Cookie": cookieA})
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status=%d headers=%v body=%s", created.Code, created.Header(), created.Body.String())
	}
	session := decodeLoginSession(t, created)
	waitHTTPLoginState(t, manager, adminauth.Fingerprint(tokenA), session.ID, managedlogin.StateInteractive)

	busy := doJSON(t, h, http.MethodPost, "/api/admin/youtube-login/sessions", nil, map[string]string{"Cookie": cookieA})
	if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "session_busy") {
		t.Fatalf("busy status=%d body=%s", busy.Code, busy.Body.String())
	}

	other := doJSON(t, h, http.MethodGet, "/api/admin/youtube-login/sessions/"+session.ID, nil, map[string]string{"Cookie": cookieB})
	if other.Code != http.StatusNotFound {
		t.Fatalf("other admin status=%d body=%s", other.Code, other.Body.String())
	}

	verified := doJSON(t, h, http.MethodPost, "/api/admin/youtube-login/sessions/"+session.ID+"/verify", nil, map[string]string{"Cookie": cookieA})
	if verified.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verified.Code, verified.Body.String())
	}
	verifiedSession := decodeLoginSession(t, verified)
	if verifiedSession.State != managedlogin.StateSynced || !verifiedSession.LoggedIn || !verifiedSession.Updated {
		t.Fatalf("verified session=%+v", verifiedSession)
	}
	for _, secret := range []string{"LOGIN_INFO", "sapisid-value", "browser-profile", "DevToolsActivePort"} {
		if strings.Contains(verified.Body.String(), secret) {
			t.Fatalf("response exposed %q: %s", secret, verified.Body.String())
		}
	}
}

func TestManagedLoginVerifySanitizesRawBrowserError(t *testing.T) {
	secret := "RAW_CDP_PASSWORD_TOKEN"
	browser := newHTTPLoginBrowser(nil)
	browser.exportErr = errors.New(secret)
	srv, manager, admin := newManagedLoginHTTPServer(t, browser, log.Printf)
	token, _ := admin.Login("adm-pass")
	cookie := adminauth.CookieName + "=" + token
	created := doJSON(t, srv.Handler(), http.MethodPost, "/api/admin/youtube-login/sessions", nil, map[string]string{"Cookie": cookie})
	session := decodeLoginSession(t, created)
	waitHTTPLoginState(t, manager, adminauth.Fingerprint(token), session.ID, managedlogin.StateInteractive)

	verified := doJSON(t, srv.Handler(), http.MethodPost, "/api/admin/youtube-login/sessions/"+session.ID+"/verify", nil, map[string]string{"Cookie": cookie})
	if verified.Code != http.StatusInternalServerError || !strings.Contains(verified.Body.String(), "sync_failed") {
		t.Fatalf("status=%d body=%s", verified.Code, verified.Body.String())
	}
	if strings.Contains(verified.Body.String(), secret) {
		t.Fatalf("raw browser error exposed: %s", verified.Body.String())
	}
}

func TestManagedLoginWebSocketAuthOriginOwnerAndSingleController(t *testing.T) {
	var logBuffer lockedLogBuffer
	oldWriter := log.Writer()
	log.SetOutput(&logBuffer)
	defer log.SetOutput(oldWriter)

	browser := newHTTPLoginBrowser(nil)
	srv, manager, admin := newManagedLoginHTTPServer(t, browser, log.Printf)
	tokenA, _ := admin.Login("adm-pass")
	tokenB, _ := admin.Login("adm-pass")
	ownerA := adminauth.Fingerprint(tokenA)
	snapshot, err := manager.Create(ownerA)
	if err != nil {
		t.Fatal(err)
	}
	waitHTTPLoginState(t, manager, ownerA, snapshot.ID, managedlogin.StateInteractive)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + snapshot.ChannelPath

	assertWSDialStatus(t, wsURL, ts.URL, "", http.StatusUnauthorized)
	assertWSDialStatus(t, wsURL, "https://cross-origin.invalid", adminauth.CookieName+"="+tokenA, http.StatusForbidden)
	assertWSDialStatus(t, wsURL, ts.URL, adminauth.CookieName+"="+tokenB, http.StatusNotFound)

	header := http.Header{}
	header.Set("Origin", ts.URL)
	header.Set("Cookie", adminauth.CookieName+"="+tokenA)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("valid websocket dial: status=%d err=%v", status, err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 2; i++ {
		messageType, data, err := conn.ReadMessage()
		if err != nil || messageType != websocket.TextMessage {
			t.Fatalf("initial message %d type=%d err=%v data=%s", i, messageType, err, data)
		}
	}

	assertWSDialStatus(t, wsURL, ts.URL, adminauth.CookieName+"="+tokenA, http.StatusConflict)

	frame := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0xff, 0xd9}
	browser.frames <- managedlogin.Frame{Data: frame, Viewport: managedlogin.Viewport{Width: 1000, Height: 700}}
	messageType, data, err := conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage || !bytes.Contains(data, []byte(`"type":"viewport"`)) {
		t.Fatalf("viewport update type=%d err=%v data=%s", messageType, err, data)
	}
	messageType, data, err = conn.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage || !bytes.Equal(data, frame) {
		t.Fatalf("frame type=%d err=%v data=%v", messageType, err, data)
	}

	secret := "front-end-password-value"
	if err := conn.WriteJSON(map[string]any{"type": "text", "text": secret}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "terminate"}); err != nil {
		t.Fatal(err)
	}
	_, _, _ = conn.ReadMessage()
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)

	browser.mu.Lock()
	inputs := append([]managedlogin.InputEvent(nil), browser.inputs...)
	browser.mu.Unlock()
	if len(inputs) != 1 || inputs[0].Text != secret {
		t.Fatalf("forwarded inputs=%+v", inputs)
	}
	if strings.Contains(logBuffer.String(), secret) {
		t.Fatalf("sensitive input entered logs: %q", logBuffer.String())
	}
	if strings.Contains(logBuffer.String(), snapshot.ID) {
		t.Fatalf("full login session id entered access logs: %q", logBuffer.String())
	}
}

func TestExactSameOriginHonorsHTTPSProxyAndRejectsHostMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/api/admin/youtube-login/sessions/x/channel", nil)
	req.Host = "music.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://music.example.com")
	if !exactSameOrigin(req) {
		t.Fatal("matching forwarded HTTPS origin should pass")
	}
	req.Host = "music.example.com:443"
	if !exactSameOrigin(req) {
		t.Fatal("default HTTPS port should be the same origin")
	}
	req.Header.Set("Origin", "https://other.example.com")
	if exactSameOrigin(req) {
		t.Fatal("host mismatch should fail")
	}
}

func assertWSDialStatus(t *testing.T, wsURL, origin, cookie string, want int) {
	t.Helper()
	header := http.Header{}
	header.Set("Origin", origin)
	if cookie != "" {
		header.Set("Cookie", cookie)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("dial unexpectedly succeeded, want status %d", want)
	}
	if resp == nil || resp.StatusCode != want {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial status=%d want=%d err=%v", status, want, err)
	}
}

func newManagedLoginHTTPServer(t *testing.T, browser *httpLoginBrowser, logf func(string, ...any)) (*Server, *managedlogin.Manager, *adminauth.Manager) {
	t.Helper()
	cfg := testCfg(t)
	cfg.AdminPassword = "adm-pass"
	cfg.AdminSessionSecret = "adm-secret"
	cfg.AdminSessionTTL = time.Hour
	cfg.CookiesDir = filepath.Join(t.TempDir(), "cookies")
	cfg.CookiesFile = filepath.Join(cfg.CookiesDir, cookies.StableFileName)
	cfg.CookieSourceMode = cookies.CookieSourceModeAuto
	admin := adminauth.New(adminauth.Options{
		Password: cfg.AdminPassword, SessionSecret: cfg.AdminSessionSecret, TTL: cfg.AdminSessionTTL,
	})
	source := cookies.NewSourceArbiter(cfg.CookieSourceMode, false)
	manager, err := managedlogin.New(managedlogin.Options{
		ProfileDir: filepath.Join(t.TempDir(), "browser-profile"),
		StableFile: cfg.CookiesFile,
		Headless:   true,
		SessionTTL: 2 * time.Second,
		Launcher:   &httpLoginLauncher{browser: browser},
		Source:     source,
		Logf:       logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	srv := newTestServer(t, cfg, nil, nil)
	srv.admin = admin
	srv.cookieSource = source
	srv.managedLogin = manager
	return srv, manager, admin
}

func decodeLoginSession(t *testing.T, rr *httptest.ResponseRecorder) managedlogin.Snapshot {
	t.Helper()
	var payload struct {
		Session managedlogin.Snapshot `json:"session"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode session: %v body=%s", err, rr.Body.String())
	}
	if payload.Session.ID == "" {
		t.Fatalf("missing session: %s", rr.Body.String())
	}
	return payload.Session
}

func waitHTTPLoginState(t *testing.T, manager *managedlogin.Manager, owner, id, want string) managedlogin.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Get(owner, id)
		if snapshot.State == want {
			return snapshot
		}
		if err != nil && !errors.Is(err, managedlogin.ErrExpired) {
			t.Fatalf("get state: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, _ := manager.Get(owner, id)
	t.Fatalf("state=%q want=%q snapshot=%+v", snapshot.State, want, snapshot)
	return managedlogin.Snapshot{}
}

func httpAuthenticatedCookies() []managedlogin.BrowserCookie {
	return []managedlogin.BrowserCookie{
		{Name: "LOGIN_INFO", Value: "login-info-value", Domain: ".youtube.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SID", Value: "sid-value", Domain: ".google.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SAPISID", Value: "sapisid-value", Domain: ".google.com", Path: "/", Secure: true},
	}
}
