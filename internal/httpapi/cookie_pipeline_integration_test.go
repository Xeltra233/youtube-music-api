package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/cookies"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

type cookiePipelineBrowserRunner struct {
	jar      string
	tempPath string
}

func (r *cookiePipelineBrowserRunner) Run(ctx context.Context, _ string, args []string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	path, ok := cookiePipelineArgValue(args, "--cookies")
	if !ok {
		return "", "", fmt.Errorf("missing browser cookie output")
	}
	r.tempPath = path
	if err := os.WriteFile(path, []byte(r.jar), 0o600); err != nil {
		return "", "", err
	}
	return "", "", nil
}

type cookiePipelineDownloadCall struct {
	format     string
	cookiePath string
	cookieBody string
}

type cookiePipelineDownloadRunner struct {
	mu    sync.Mutex
	calls []cookiePipelineDownloadCall
}

func (r *cookiePipelineDownloadRunner) Run(
	ctx context.Context,
	_ string,
	args []string,
	_ []string,
) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	cookiePath, ok := cookiePipelineArgValue(args, "--cookies")
	if !ok {
		return "", "", fmt.Errorf("missing download cookie input")
	}
	cookieBody, err := os.ReadFile(cookiePath)
	if err != nil {
		return "", "", err
	}
	format := "mp3"
	if value, found := cookiePipelineArgValue(args, "--audio-format"); found {
		format = value
	}
	if value, found := cookiePipelineArgValue(args, "--merge-output-format"); found {
		format = value
	}
	output, ok := cookiePipelineArgValue(args, "--output")
	if !ok {
		return "", "", fmt.Errorf("missing download output")
	}
	output = strings.ReplaceAll(output, "%(ext)s", format)
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(output, bytes.Repeat([]byte("media"), 512), 0o600); err != nil {
		return "", "", err
	}

	// Simulate yt-dlp rewriting its --cookies input. The stable jar must stay
	// byte-for-byte unchanged because downloads receive disposable snapshots.
	f, err := os.OpenFile(cookiePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", err
	}
	_, writeErr := f.WriteString(".youtube.com\tTRUE\t/\tFALSE\t0\tVISITOR_INFO1_LIVE\tSNAPSHOT_ONLY\n")
	closeErr := f.Close()
	if writeErr != nil {
		return "", "", writeErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}

	r.mu.Lock()
	r.calls = append(r.calls, cookiePipelineDownloadCall{
		format:     format,
		cookiePath: cookiePath,
		cookieBody: string(cookieBody),
	})
	r.mu.Unlock()
	return "ok", "", nil
}

func (r *cookiePipelineDownloadRunner) snapshotCalls() []cookiePipelineDownloadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cookiePipelineDownloadCall(nil), r.calls...)
}

type cookiePipelineProbeRunner struct{}

func (cookiePipelineProbeRunner) Run(
	ctx context.Context,
	_ string,
	_ []string,
	_ []string,
) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return "video,1920,1080\n", "", nil
}

func TestCookiePipelineBrowserSyncAndUploadFallback(t *testing.T) {
	root := t.TempDir()
	cookiesDir := filepath.Join(root, "cookies")
	stable := filepath.Join(cookiesDir, cookies.StableFileName)
	if err := os.MkdirAll(cookiesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var searchMu sync.Mutex
	var searchCookieHeaders []string
	searchUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchMu.Lock()
		searchCookieHeaders = append(searchCookieHeaders, r.Header.Get("Cookie"))
		searchMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(searchUpstream.Close)

	searchClient, err := ytmusic.New(ytmusic.Options{
		BaseURL:     searchUpstream.URL,
		HTTPClient:  searchUpstream.Client(),
		CookiesFile: stable,
		APIKey:      "integration-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	downloadDir := filepath.Join(root, "downloads")
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
	downloadCfg := &config.Config{
		DownloadDir:            downloadDir,
		AudioFormat:            "mp3",
		AudioBitrate:           "192",
		YtdlpPath:              fakeYtdlp,
		FFmpegLocation:         mediaTools,
		MaxConcurrentDownloads: 2,
		MaxFilesizeMB:          50,
		DownloadTimeout:        5 * time.Second,
		CacheTTL:               time.Hour,
		CacheMaxTotalMB:        100,
		CookiesDir:             cookiesDir,
		CookiesFile:            stable,
	}
	downloadRunner := &cookiePipelineDownloadRunner{}
	downloader, err := download.New(downloadCfg, download.Options{
		Runner:      downloadRunner,
		ProbeRunner: cookiePipelineProbeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}

	browserJar := cookiePipelineJar("BROWSER_PIPELINE")
	browserRunner := &cookiePipelineBrowserRunner{jar: browserJar}
	syncResult, err := cookies.NewBrowserSyncer(browserRunner).Sync(context.Background(), cookies.BrowserSyncOptions{
		BrowserSpec: "chrome:Integration Profile",
		StableFile:  stable,
		YtdlpPath:   fakeYtdlp,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("browser sync: %v", err)
	}
	if !syncResult.Updated || !syncResult.LoggedIn {
		t.Fatalf("browser sync result: %+v", syncResult)
	}
	if browserRunner.tempPath == "" {
		t.Fatal("browser runner did not receive a temporary jar")
	}
	if _, err := os.Stat(browserRunner.tempPath); !os.IsNotExist(err) {
		t.Fatalf("browser temporary jar was not cleaned: %v", err)
	}

	exerciseCookieConsumers(t, searchClient, downloader, downloadRunner, &searchMu, &searchCookieHeaders,
		"browser sync", "BROWSER_PIPELINE", browserJar, stable, cookiesDir, "audioA1", "videoA1")

	// The management upload remains a real runtime fallback. Reuse the same
	// search client and downloader to prove both re-read the stable path.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stable, old, old); err != nil {
		t.Fatal(err)
	}
	adminCfg := testCfg(t)
	adminCfg.AdminPassword = "integration-admin"
	adminCfg.AdminSessionSecret = "integration-secret"
	adminCfg.AdminSessionTTL = time.Hour
	adminCfg.CookiesDir = cookiesDir
	adminCfg.CookiesFile = stable
	adminCfg.CookiesFromBrowser = "chrome:Integration Profile"
	adminServer := newTestServer(t, adminCfg, nil, nil)
	adminHandler := adminServer.Handler()

	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"password":"integration-admin"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	adminHandler.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("admin login: status=%d body=%s", login.Code, login.Body.String())
	}
	adminCookie := cookieHeaderFromSetCookie(login.Result().Header.Get("Set-Cookie"))
	uploadJar := cookiePipelineJar("UPLOAD_FALLBACK")
	var uploadBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&uploadBody)
	part, err := multipartWriter.CreateFormFile("file", cookies.StableFileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(uploadJar)); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRecorder()
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/admin/cookies/upload", &uploadBody)
	uploadReq.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	uploadReq.Header.Set("Cookie", adminCookie)
	adminHandler.ServeHTTP(upload, uploadReq)
	if upload.Code != http.StatusOK {
		t.Fatalf("admin upload: status=%d body=%s", upload.Code, upload.Body.String())
	}
	for _, marker := range []string{"BROWSER_PIPELINE", "UPLOAD_FALLBACK"} {
		if strings.Contains(upload.Body.String(), marker) {
			t.Fatalf("admin upload response leaked cookie marker %q", marker)
		}
	}

	exerciseCookieConsumers(t, searchClient, downloader, downloadRunner, &searchMu, &searchCookieHeaders,
		"file upload fallback", "UPLOAD_FALLBACK", uploadJar, stable, cookiesDir, "audioB2", "videoB2")
}

func exerciseCookieConsumers(
	t *testing.T,
	searchClient *ytmusic.Client,
	downloader *download.Downloader,
	runner *cookiePipelineDownloadRunner,
	searchMu *sync.Mutex,
	searchHeaders *[]string,
	phase string,
	marker string,
	wantStable string,
	stable string,
	cookiesDir string,
	audioVideoID string,
	mp4VideoID string,
) {
	t.Helper()
	searchMu.Lock()
	searchBefore := len(*searchHeaders)
	searchMu.Unlock()
	if _, err := searchClient.Search(context.Background(), "cookie pipeline fixture"); err != nil {
		t.Fatalf("%s search: %v", phase, err)
	}
	searchMu.Lock()
	if len(*searchHeaders) != searchBefore+1 {
		searchMu.Unlock()
		t.Fatalf("%s search header count did not advance", phase)
	}
	header := (*searchHeaders)[len(*searchHeaders)-1]
	searchMu.Unlock()
	if !strings.Contains(header, "LOGIN_INFO="+marker+"_LOGIN") ||
		!strings.Contains(header, "SID="+marker+"_SID") ||
		!strings.Contains(header, "SAPISID="+marker+"_SAPISID") {
		t.Fatalf("%s search did not consume expected stable jar: %q", phase, header)
	}

	callsBefore := len(runner.snapshotCalls())
	if _, err := downloader.Download(context.Background(), download.Request{
		VideoID: audioVideoID,
		Format:  "mp3",
		Title:   phase + " audio",
	}); err != nil {
		t.Fatalf("%s audio download: %v", phase, err)
	}
	if _, err := downloader.Download(context.Background(), download.Request{
		VideoID: mp4VideoID,
		Format:  "mp4",
		Title:   phase + " video",
	}); err != nil {
		t.Fatalf("%s mp4 download: %v", phase, err)
	}
	calls := runner.snapshotCalls()
	if len(calls) != callsBefore+2 {
		t.Fatalf("%s download calls=%d want %d", phase, len(calls)-callsBefore, 2)
	}
	gotFormats := map[string]bool{}
	for _, call := range calls[callsBefore:] {
		gotFormats[call.format] = true
		if filepath.Clean(call.cookiePath) == filepath.Clean(stable) {
			t.Fatalf("%s %s download received the stable jar directly", phase, call.format)
		}
		if !strings.Contains(call.cookieBody, marker+"_LOGIN") ||
			!strings.Contains(call.cookieBody, marker+"_SID") ||
			!strings.Contains(call.cookieBody, marker+"_SAPISID") {
			t.Fatalf("%s %s snapshot did not contain expected cookie generation", phase, call.format)
		}
		if _, err := os.Stat(call.cookiePath); !os.IsNotExist(err) {
			t.Fatalf("%s %s snapshot was not cleaned: %v", phase, call.format, err)
		}
	}
	if !gotFormats["mp3"] || !gotFormats["mp4"] {
		t.Fatalf("%s formats observed: %v", phase, gotFormats)
	}
	stableBody, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(stableBody) != wantStable {
		t.Fatalf("%s stable jar changed after consumers", phase)
	}
	if strings.Contains(string(stableBody), "SNAPSHOT_ONLY") {
		t.Fatalf("%s yt-dlp snapshot rewrite reached stable jar", phase)
	}
	for _, pattern := range []string{".browser-cookies-*.tmp", ".ytdlp-cookies-*.tmp"} {
		matches, err := filepath.Glob(filepath.Join(cookiesDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("%s left temporary cookie files: %v", phase, matches)
		}
	}
}

func cookiePipelineJar(marker string) string {
	return "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\t" + marker + "_LOGIN\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSID\t" + marker + "_SID\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSAPISID\t" + marker + "_SAPISID\n"
}

func cookiePipelineArgValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}
