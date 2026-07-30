package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xeltra/ytmusic-bridge/internal/cookies"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/managedlogin"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

const (
	managedPipelineVideoA = "MngdPipeA01"
	managedPipelineVideoB = "MngdPipeB02"
)

type managedPipelineLauncher struct {
	mu       sync.Mutex
	browsers []*managedPipelineBrowser
	options  []managedlogin.LaunchOptions
}

func (l *managedPipelineLauncher) Launch(ctx context.Context, opt managedlogin.LaunchOptions) (managedlogin.Browser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(opt.ProfileDir, "Default"), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opt.ProfileDir, "Default", "Preferences"), []byte("{}"), 0o600); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.options = append(l.options, opt)
	if len(l.browsers) == 0 {
		return nil, fmt.Errorf("managed pipeline browser queue exhausted")
	}
	browser := l.browsers[0]
	l.browsers = l.browsers[1:]
	return browser, nil
}

func (l *managedPipelineLauncher) snapshotOptions() []managedlogin.LaunchOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]managedlogin.LaunchOptions(nil), l.options...)
}

type managedPipelineBrowser struct {
	mu                   sync.Mutex
	frames               chan managedlogin.Frame
	done                 chan error
	closeOnce            sync.Once
	viewport             managedlogin.Viewport
	expectedInputs       []string
	inputIndex           int
	authenticatedCookies []managedlogin.BrowserCookie
	cookies              []managedlogin.BrowserCookie
	clearCalls           int
}

func newManagedPipelineBrowser(
	initialCookies []managedlogin.BrowserCookie,
	expectedInputs []string,
	authenticatedCookies []managedlogin.BrowserCookie,
) *managedPipelineBrowser {
	frames := make(chan managedlogin.Frame, 1)
	frames <- managedlogin.Frame{
		Data:     []byte("SYNTHETIC_MANAGED_LOGIN_FRAME"),
		Viewport: managedlogin.Viewport{Width: 1280, Height: 800},
	}
	return &managedPipelineBrowser{
		frames:               frames,
		done:                 make(chan error, 1),
		viewport:             managedlogin.Viewport{Width: 1280, Height: 800},
		expectedInputs:       append([]string(nil), expectedInputs...),
		authenticatedCookies: append([]managedlogin.BrowserCookie(nil), authenticatedCookies...),
		cookies:              append([]managedlogin.BrowserCookie(nil), initialCookies...),
	}
}

func (b *managedPipelineBrowser) Frames() <-chan managedlogin.Frame { return b.frames }
func (b *managedPipelineBrowser) Done() <-chan error                { return b.done }

func (b *managedPipelineBrowser) Viewport() managedlogin.Viewport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.viewport
}

func (b *managedPipelineBrowser) Dispatch(_ context.Context, event managedlogin.InputEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if event.Kind != "text" || len(b.expectedInputs) == 0 {
		return nil
	}
	if b.inputIndex >= len(b.expectedInputs) || event.Text != b.expectedInputs[b.inputIndex] {
		return fmt.Errorf("isolated login input sequence rejected")
	}
	b.inputIndex++
	if b.inputIndex == len(b.expectedInputs) {
		b.cookies = append([]managedlogin.BrowserCookie(nil), b.authenticatedCookies...)
	}
	return nil
}

func (b *managedPipelineBrowser) Resize(_ context.Context, viewport managedlogin.Viewport) error {
	b.mu.Lock()
	b.viewport = viewport
	b.mu.Unlock()
	return nil
}

func (b *managedPipelineBrowser) ExportCookies(context.Context) ([]managedlogin.BrowserCookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]managedlogin.BrowserCookie(nil), b.cookies...), nil
}

func (b *managedPipelineBrowser) ClearCookies(context.Context) error {
	b.mu.Lock()
	b.clearCalls++
	b.cookies = nil
	b.mu.Unlock()
	return nil
}

func (b *managedPipelineBrowser) Close(context.Context) error {
	b.closeOnce.Do(func() {
		b.done <- nil
		close(b.done)
	})
	return nil
}

func (b *managedPipelineBrowser) inputCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inputIndex
}

func (b *managedPipelineBrowser) clearCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clearCalls
}

type managedPipelineSearchCapture struct {
	mu      sync.Mutex
	headers []string
	videoID string
}

func (c *managedPipelineSearchCapture) setVideoID(videoID string) {
	c.mu.Lock()
	c.videoID = videoID
	c.mu.Unlock()
}

func (c *managedPipelineSearchCapture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.headers = append(c.headers, r.Header.Get("Cookie"))
	videoID := c.videoID
	c.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"x": []any{map[string]any{
			"musicResponsiveListItemRenderer": map[string]any{
				"playlistItemData": map[string]any{"videoId": videoID},
				"flexColumns": []any{
					map[string]any{"musicResponsiveListItemFlexColumnRenderer": map[string]any{
						"text": map[string]any{"runs": []any{map[string]any{"text": "Managed Login Fixture Song"}}},
					}},
					map[string]any{"musicResponsiveListItemFlexColumnRenderer": map[string]any{
						"text": map[string]any{"runs": []any{
							map[string]any{"text": "Fixture Artist"},
							map[string]any{"text": " • "},
							map[string]any{"text": "3:21"},
						}},
					}},
				},
			},
		}},
	})
}

func (c *managedPipelineSearchCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.headers)
}

func (c *managedPipelineSearchCapture) latestHeader() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.headers) == 0 {
		return ""
	}
	return c.headers[len(c.headers)-1]
}

type managedPipelineSearchUpstream struct {
	client *ytmusic.Client
}

func (u managedPipelineSearchUpstream) Search(ctx context.Context, query string) ([]ytmusic.Track, error) {
	return u.client.Search(ctx, query)
}

func TestManagedFrontendLoginCookiePipelineEndToEnd(t *testing.T) {
	root := t.TempDir()
	cookiesDir := filepath.Join(root, "cookies")
	stable := filepath.Join(cookiesDir, cookies.StableFileName)
	profileDir := filepath.Join(root, "browser-profile")
	downloadDir := filepath.Join(root, "downloads")
	for _, dir := range []string{cookiesDir, downloadDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	searchCapture := &managedPipelineSearchCapture{videoID: managedPipelineVideoA}
	searchUpstream := httptest.NewServer(http.HandlerFunc(searchCapture.serveHTTP))
	t.Cleanup(searchUpstream.Close)
	searchClient, err := ytmusic.New(ytmusic.Options{
		BaseURL:     searchUpstream.URL,
		HTTPClient:  searchUpstream.Client(),
		CookiesFile: stable,
		APIKey:      "managed-pipeline-api-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testCfg(t)
	cfg.AdminPassword = "ADMIN_PASSWORD_FIXTURE"
	cfg.AdminSessionSecret = "ADMIN_SESSION_FIXTURE"
	cfg.AdminSessionTTL = time.Hour
	cfg.DefaultLimit = 5
	cfg.MaxLimit = 10
	cfg.MinScore = 0
	cfg.SearchTimeout = 5 * time.Second
	cfg.SessionTTL = time.Minute
	cfg.DownloadDir = downloadDir
	cfg.DownloadTimeout = 5 * time.Second
	cfg.CookiesDir = cookiesDir
	cfg.CookiesFile = stable
	cfg.CookieSourceMode = cookies.CookieSourceModeAuto

	mediaTools := filepath.Join(root, "media-tools")
	if err := os.MkdirAll(mediaTools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(mediaTools, name), []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeYtdlp := filepath.Join(root, "yt-dlp-fixture.exe")
	if err := os.WriteFile(fakeYtdlp, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeYtdlp
	cfg.FFmpegLocation = mediaTools
	cfg.AudioFormat = "mp3"
	cfg.AudioBitrate = "192"
	cfg.MaxConcurrentDownloads = 2
	cfg.MaxFilesizeMB = 50
	cfg.CacheTTL = time.Hour
	cfg.CacheMaxTotalMB = 100

	downloaderRunner := &cookiePipelineDownloadRunner{}
	downloader, err := download.New(cfg, download.Options{
		Runner:      downloaderRunner,
		ProbeRunner: cookiePipelineProbeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	searchService, err := search.New(managedPipelineSearchUpstream{client: searchClient}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	managedMarker := "MANAGED_FRONTEND_E2E"
	fallbackMarker := "UPLOAD_FALLBACK_E2E"
	credentials := []string{"ACCOUNT_FIXTURE", "PASSWORD_FIXTURE", "OTP_FIXTURE"}
	allFixtureSecrets := append([]string{}, credentials...)
	allFixtureSecrets = append(allFixtureSecrets, cfg.AdminPassword)
	allFixtureSecrets = append(allFixtureSecrets, managedPipelineSecretValues(managedMarker)...)
	allFixtureSecrets = append(allFixtureSecrets, managedPipelineSecretValues(fallbackMarker)...)
	authCookies := managedPipelineCookies(managedMarker)
	authBrowser := newManagedPipelineBrowser(nil, credentials, authCookies)
	refreshBrowser := newManagedPipelineBrowser(authCookies, nil, nil)
	clearBrowser := newManagedPipelineBrowser(authCookies, nil, nil)
	invalidBrowser := newManagedPipelineBrowser([]managedlogin.BrowserCookie{
		{Name: "VISITOR_INFO1_LIVE", Value: "ANONYMOUS_FIXTURE", Domain: ".youtube.com", Path: "/"},
	}, nil, nil)
	launcher := &managedPipelineLauncher{browsers: []*managedPipelineBrowser{
		authBrowser, refreshBrowser, clearBrowser, invalidBrowser,
	}}
	source := cookies.NewSourceArbiter(cookies.CookieSourceModeAuto, false)
	var logs lockedLogBuffer
	manager, err := managedlogin.New(managedlogin.Options{
		ProfileDir:      profileDir,
		StableFile:      stable,
		Headless:        true,
		SessionTTL:      time.Minute,
		RefreshInterval: 0,
		Launcher:        launcher,
		Source:          source,
		Logf:            log.New(&logs, "", 0).Printf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)

	server, err := New(Options{
		Config:       cfg,
		Searcher:     searchService,
		Sessions:     session.NewStore(session.Options{TTL: cfg.SessionTTL}),
		Downloader:   downloader,
		CookieSource: source,
		ManagedLogin: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := testServer.Client()
	client.Jar = jar

	loginPage, err := client.Get(testServer.URL + "/admin/login.html")
	if err != nil {
		t.Fatal(err)
	}
	loginPageBody, readErr := io.ReadAll(loginPage.Body)
	_ = loginPage.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if loginPage.StatusCode != http.StatusOK || !bytes.Contains(loginPageBody, []byte("YouTube 认证管理")) {
		t.Fatalf("admin login page status=%d", loginPage.StatusCode)
	}

	status, loginBody := managedPipelineDoJSON(t, client, http.MethodPost, testServer.URL+"/api/admin/login",
		map[string]any{"password": cfg.AdminPassword}, nil)
	if status != http.StatusOK {
		t.Fatalf("admin login status=%d", status)
	}
	managedPipelineAssertNoValues(t, loginBody, append(credentials, cfg.AdminPassword))

	var created managedPipelineSessionPayload
	status, createdBody := managedPipelineDoJSON(t, client, http.MethodPost,
		testServer.URL+"/api/admin/youtube-login/sessions", nil, &created)
	if status != http.StatusCreated || created.Session.ID == "" {
		t.Fatalf("create managed session status=%d", status)
	}
	managedPipelineAssertNoValues(t, createdBody, managedPipelineSecretValues(managedMarker))
	interactive := managedPipelineWaitSession(t, client, testServer.URL, created.Session.ID, managedlogin.StateInteractive)

	managedPipelineDriveLoginWebSocket(t, client, testServer.URL, interactive, credentials,
		append(credentials, managedPipelineSecretValues(managedMarker)...))
	if authBrowser.inputCount() != len(credentials) {
		t.Fatalf("isolated login inputs=%d want=%d", authBrowser.inputCount(), len(credentials))
	}
	stableAfterFrontendLogin, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range managedPipelineSecretValues(managedMarker) {
		if !bytes.Contains(stableAfterFrontendLogin, []byte(value)) {
			t.Fatal("frontend login did not commit the authenticated cookie generation")
		}
	}

	refreshResult, err := manager.RefreshOnce(context.Background())
	if err != nil || !refreshResult.LoggedIn {
		t.Fatalf("managed profile restart refresh result=%+v err=%v", refreshResult, err)
	}
	launches := launcher.snapshotOptions()
	if len(launches) < 2 || !launches[0].Screencast || launches[1].Screencast ||
		filepath.Clean(launches[0].ProfileDir) != filepath.Clean(launches[1].ProfileDir) {
		t.Fatalf("managed profile launch sequence=%+v", launches)
	}

	managedStatus := managedPipelineCookieStatus(t, client, testServer.URL, allFixtureSecrets)
	if managedStatus["source"] != cookies.CookieSourceManaged || managedStatus["managed_authenticated"] != true ||
		managedStatus["logged_in"] != true || managedStatus["auth_cookie_count"] != float64(3) {
		t.Fatalf("managed cookie status metadata=%v", managedStatus)
	}
	managedPipelineExerciseHTTPConsumers(t, client, testServer.URL, searchCapture, downloaderRunner,
		"managed login", managedMarker, allFixtureSecrets, managedPipelineVideoA, stable, cookiesDir)

	managedStableBefore, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	fallbackJar := cookiePipelineJar(fallbackMarker)
	uploadStatus, uploadBody := managedPipelineUpload(t, client, testServer.URL, fallbackJar)
	if uploadStatus != http.StatusOK {
		t.Fatalf("fallback upload status=%d", uploadStatus)
	}
	managedPipelineAssertNoValues(t, uploadBody,
		append(managedPipelineSecretValues(managedMarker), managedPipelineSecretValues(fallbackMarker)...))
	managedStableAfterUpload, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(managedStableBefore, managedStableAfterUpload) {
		t.Fatal("fallback upload replaced the active managed stable jar")
	}
	stillManaged := managedPipelineCookieStatus(t, client, testServer.URL, allFixtureSecrets)
	if stillManaged["source"] != cookies.CookieSourceManaged || stillManaged["managed_authenticated"] != true {
		t.Fatalf("source changed during managed fallback upload: %v", stillManaged)
	}

	status, disconnectBody := managedPipelineDoJSON(t, client, http.MethodPost,
		testServer.URL+"/api/admin/youtube-login/disconnect", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("managed disconnect status=%d", status)
	}
	managedPipelineAssertNoValues(t, disconnectBody,
		append(managedPipelineSecretValues(managedMarker), managedPipelineSecretValues(fallbackMarker)...))
	if clearBrowser.clearCount() != 1 {
		t.Fatalf("profile clear calls=%d want=1", clearBrowser.clearCount())
	}
	fallbackStable, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(fallbackStable) != fallbackJar {
		t.Fatal("disconnect did not promote the uploaded fallback jar")
	}
	fallbackStatus := managedPipelineCookieStatus(t, client, testServer.URL, allFixtureSecrets)
	if fallbackStatus["source"] != cookies.CookieSourceFile || fallbackStatus["managed_authenticated"] != false ||
		fallbackStatus["logged_in"] != true {
		t.Fatalf("fallback cookie status metadata=%v", fallbackStatus)
	}

	searchCapture.setVideoID(managedPipelineVideoB)
	managedPipelineExerciseHTTPConsumers(t, client, testServer.URL, searchCapture, downloaderRunner,
		"uploaded fallback", fallbackMarker, allFixtureSecrets, managedPipelineVideoB, stable, cookiesDir)

	var invalidCreated managedPipelineSessionPayload
	status, invalidCreatedBody := managedPipelineDoJSON(t, client, http.MethodPost,
		testServer.URL+"/api/admin/youtube-login/sessions", nil, &invalidCreated)
	if status != http.StatusCreated || invalidCreated.Session.ID == "" {
		t.Fatalf("invalid managed session create status=%d", status)
	}
	managedPipelineAssertNoValues(t, invalidCreatedBody, managedPipelineSecretValues(fallbackMarker))
	managedPipelineWaitSession(t, client, testServer.URL, invalidCreated.Session.ID, managedlogin.StateInteractive)
	status, invalidVerifyBody := managedPipelineDoJSON(t, client, http.MethodPost,
		testServer.URL+"/api/admin/youtube-login/sessions/"+invalidCreated.Session.ID+"/verify", nil, nil)
	if status != http.StatusConflict || !bytes.Contains(invalidVerifyBody, []byte("not_logged_in")) {
		t.Fatalf("invalid managed verify status=%d", status)
	}
	managedPipelineAssertNoValues(t, invalidVerifyBody, managedPipelineSecretValues(fallbackMarker))
	managedPipelineWaitSession(t, client, testServer.URL, invalidCreated.Session.ID, managedlogin.StateNotLoggedIn)
	afterInvalid := managedPipelineCookieStatus(t, client, testServer.URL, allFixtureSecrets)
	if afterInvalid["source"] != cookies.CookieSourceFile || afterInvalid["managed_authenticated"] != false ||
		afterInvalid["logged_in"] != true {
		t.Fatalf("invalid managed profile changed fallback metadata=%v", afterInvalid)
	}
	finalStable, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(finalStable) != fallbackJar {
		t.Fatal("invalid managed verification changed the fallback stable jar")
	}

	managedPipelineAssertNoValues(t, []byte(logs.String()), allFixtureSecrets)
	managedPipelineAssertNoCookieTemps(t, cookiesDir)
}

type managedPipelineSessionPayload struct {
	OK      bool                  `json:"ok"`
	Session managedlogin.Snapshot `json:"session"`
}

func managedPipelineDriveLoginWebSocket(
	t *testing.T,
	client *http.Client,
	baseURL string,
	snapshot managedlogin.Snapshot,
	inputs []string,
	secrets []string,
) {
	t.Helper()
	base, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	cookieParts := make([]string, 0)
	for _, cookie := range client.Jar.Cookies(base) {
		cookieParts = append(cookieParts, cookie.Name+"="+cookie.Value)
	}
	header := http.Header{}
	header.Set("Origin", baseURL)
	header.Set("Cookie", strings.Join(cookieParts, "; "))
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + snapshot.ChannelPath
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("managed login websocket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	seenFrame := false
	for i := 0; i < 5 && !seenFrame; i++ {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read initial managed login channel: %v", err)
		}
		managedPipelineAssertNoValues(t, data, secrets)
		seenFrame = messageType == websocket.BinaryMessage && len(data) > 0
	}
	if !seenFrame {
		t.Fatal("managed login channel did not emit a browser frame")
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "mouse", "event_type": "mousePressed", "x": 0.5, "y": 0.4,
		"button": "left", "buttons": 1, "click_count": 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if err := conn.WriteJSON(map[string]any{"type": "text", "text": input}); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteJSON(map[string]any{"type": "verify"}); err != nil {
		t.Fatal(err)
	}

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read managed login verify result: %v", err)
		}
		managedPipelineAssertNoValues(t, data, secrets)
		if messageType != websocket.TextMessage {
			continue
		}
		var message struct {
			Type    string                `json:"type"`
			Code    string                `json:"code"`
			Session managedlogin.Snapshot `json:"session"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatalf("decode managed login channel message: %v", err)
		}
		if message.Type == "error" {
			t.Fatalf("managed login channel error code=%s", message.Code)
		}
		if message.Type == "status" && message.Session.State == managedlogin.StateSynced {
			if !message.Session.LoggedIn || !message.Session.Updated || message.Session.AuthCookieCount != 3 {
				t.Fatalf("managed login synced metadata=%+v", message.Session)
			}
			return
		}
	}
}

func managedPipelineWaitSession(
	t *testing.T,
	client *http.Client,
	baseURL string,
	id string,
	want string,
) managedlogin.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var payload managedPipelineSessionPayload
		status, _ := managedPipelineDoJSON(t, client, http.MethodGet,
			baseURL+"/api/admin/youtube-login/sessions/"+id, nil, &payload)
		if status == http.StatusOK && payload.Session.State == want {
			return payload.Session
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("managed login session %s did not reach %s", id, want)
	return managedlogin.Snapshot{}
}

func managedPipelineCookieStatus(t *testing.T, client *http.Client, baseURL string, secrets []string) map[string]any {
	t.Helper()
	var payload map[string]any
	status, body := managedPipelineDoJSON(t, client, http.MethodGet,
		baseURL+"/api/admin/cookies/status", nil, &payload)
	if status != http.StatusOK {
		t.Fatalf("cookie status=%d", status)
	}
	managedPipelineAssertNoValues(t, body, secrets)
	return payload
}

func managedPipelineExerciseHTTPConsumers(
	t *testing.T,
	client *http.Client,
	baseURL string,
	searchCapture *managedPipelineSearchCapture,
	runner *cookiePipelineDownloadRunner,
	phase string,
	marker string,
	responseSecrets []string,
	videoID string,
	stable string,
	cookiesDir string,
) {
	t.Helper()
	searchBefore := searchCapture.count()
	var searchResponse SearchResponseBody
	status, searchBody := managedPipelineDoJSON(t, client, http.MethodPost, baseURL+"/search",
		map[string]any{"query": "managed login fixture", "limit": 1}, &searchResponse)
	if status != http.StatusOK || searchResponse.SessionID == "" || len(searchResponse.Results) != 1 ||
		searchResponse.Results[0].VideoID != videoID {
		t.Fatalf("%s search metadata status=%d response=%+v", phase, status, searchResponse)
	}
	managedPipelineAssertNoValues(t, searchBody, responseSecrets)
	if searchCapture.count() != searchBefore+1 {
		t.Fatalf("%s search upstream calls did not advance", phase)
	}
	header := searchCapture.latestHeader()
	for _, value := range managedPipelineSecretValues(marker) {
		if !strings.Contains(header, value) {
			t.Fatalf("%s search did not consume expected cookie generation", phase)
		}
	}

	stableBefore, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := len(runner.snapshotCalls())
	for _, format := range []string{"mp3", "mp4"} {
		var result DownloadJSONBody
		status, downloadBody := managedPipelineDoJSON(t, client, http.MethodPost,
			baseURL+"/download?mode=json", map[string]any{
				"session_id": searchResponse.SessionID,
				"index":      1,
				"format":     format,
			}, &result)
		if status != http.StatusOK || result.Format != format || result.VideoID != videoID || result.FileURL == "" {
			t.Fatalf("%s %s download metadata status=%d result=%+v", phase, format, status, result)
		}
		managedPipelineAssertNoValues(t, downloadBody, responseSecrets)
		response, err := client.Get(baseURL + result.FileURL)
		if err != nil {
			t.Fatal(err)
		}
		media, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || len(media) == 0 {
			t.Fatalf("%s %s file response status=%d err=%v", phase, format, response.StatusCode, readErr)
		}
	}

	calls := runner.snapshotCalls()
	if len(calls) != callsBefore+2 {
		t.Fatalf("%s download runner calls=%d want=2", phase, len(calls)-callsBefore)
	}
	formats := map[string]bool{}
	for _, call := range calls[callsBefore:] {
		formats[call.format] = true
		if filepath.Clean(call.cookiePath) == filepath.Clean(stable) {
			t.Fatalf("%s %s received the stable jar directly", phase, call.format)
		}
		for _, value := range managedPipelineSecretValues(marker) {
			if !strings.Contains(call.cookieBody, value) {
				t.Fatalf("%s %s snapshot missed expected cookie generation", phase, call.format)
			}
		}
		if _, err := os.Stat(call.cookiePath); !os.IsNotExist(err) {
			t.Fatalf("%s %s cookie snapshot was not cleaned: %v", phase, call.format, err)
		}
	}
	if !formats["mp3"] || !formats["mp4"] {
		t.Fatalf("%s observed download formats=%v", phase, formats)
	}
	stableAfter, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stableBefore, stableAfter) || bytes.Contains(stableAfter, []byte("SNAPSHOT_ONLY")) {
		t.Fatalf("%s stable jar changed while consumers ran", phase)
	}
	managedPipelineAssertNoCookieTemps(t, cookiesDir)
}

func managedPipelineUpload(t *testing.T, client *http.Client, baseURL, jar string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", cookies.StableFileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(jar)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/admin/cookies/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func managedPipelineDoJSON(
	t *testing.T,
	client *http.Client,
	method string,
	url string,
	body any,
	out any,
) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return response.StatusCode, data
}

func managedPipelineCookies(marker string) []managedlogin.BrowserCookie {
	return []managedlogin.BrowserCookie{
		{Name: "LOGIN_INFO", Value: marker + "_LOGIN", Domain: ".youtube.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SID", Value: marker + "_SID", Domain: ".google.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SAPISID", Value: marker + "_SAPISID", Domain: ".google.com", Path: "/", Secure: true},
	}
}

func managedPipelineSecretValues(marker string) []string {
	return []string{marker + "_LOGIN", marker + "_SID", marker + "_SAPISID"}
}

func managedPipelineAssertNoValues(t *testing.T, data []byte, values []string) {
	t.Helper()
	for _, value := range values {
		if value != "" && bytes.Contains(data, []byte(value)) {
			t.Fatalf("response or log exposed a fixture credential/cookie value")
		}
	}
}

func managedPipelineAssertNoCookieTemps(t *testing.T, cookiesDir string) {
	t.Helper()
	for _, pattern := range []string{
		".managed-cookies-*.tmp",
		".browser-cookies-*.tmp",
		".ytdlp-cookies-*.tmp",
		".youtube.txt.tmp-*",
		".youtube.txt.bak-*",
		"*.uploading",
	} {
		matches, err := filepath.Glob(filepath.Join(cookiesDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("temporary cookie files remain for pattern %s", pattern)
		}
	}
}
