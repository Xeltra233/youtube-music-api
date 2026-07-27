package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
)

// ---- stubs ----

type stubSearcher struct {
	resp *search.Response
	err  error
	last search.Request
}

func (s *stubSearcher) Search(ctx context.Context, req search.Request) (*search.Response, error) {
	s.last = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

type stubDownloader struct {
	mu      sync.Mutex
	result  *download.Result
	err     error
	calls   int
	byToken map[string]*download.Result
	lastReq download.Request
}

func (d *stubDownloader) Download(ctx context.Context, req download.Request) (*download.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	d.lastReq = req
	if d.err != nil {
		return nil, d.err
	}
	if d.result == nil {
		return nil, errors.New("no result")
	}
	out := *d.result
	return &out, nil
}

func (d *stubDownloader) LookupToken(token string) (*download.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byToken != nil {
		if r, ok := d.byToken[token]; ok {
			out := *r
			return &out, nil
		}
	}
	if d.result != nil && d.result.Token == token {
		out := *d.result
		return &out, nil
	}
	return nil, &download.NotFoundError{Reason: "token not found"}
}

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		Host:                   "127.0.0.1",
		Port:                   8787,
		APIKey:                 "",
		DefaultLimit:           10,
		MaxLimit:               20,
		MinScore:               0,
		DownloadDir:            dir,
		AudioFormat:            "mp3",
		AudioBitrate:           "192",
		MaxConcurrentDownloads: 2,
		MaxFilesizeMB:          50,
		DownloadTimeout:        time.Minute,
		SessionTTL:             30 * time.Minute,
		CacheTTL:               time.Hour,
		SearchTimeout:          5 * time.Second,
	}
}

func sampleItems() []search.Item {
	return []search.Item{
		{
			Index: 1, DisplayName: "Lemon - Kenshi Yonezu", Title: "Lemon",
			Artists: []string{"Kenshi Yonezu"}, Album: "STRAY SHEEP",
			Duration: "4:17", DurationSeconds: 257, VideoID: "3NNhrqHZqlI",
			Thumbnail: "https://example.com/a.jpg", MatchScore: 1.0,
		},
		{
			Index: 2, DisplayName: "晴天 - 周杰伦", Title: "晴天",
			Artists: []string{"周杰伦"}, Album: "莫杰他",
			Duration: "4:30", DurationSeconds: 270, VideoID: "SJKoWAd5ySo",
			Thumbnail: "https://example.com/b.jpg", MatchScore: 0.9,
		},
		{
			Index: 3, DisplayName: "Same - A", Title: "Same",
			Artists: []string{"A"}, VideoID: "aaaaaa1", MatchScore: 0.5,
		},
		{
			Index: 4, DisplayName: "Same - B", Title: "Same",
			Artists: []string{"B"}, VideoID: "bbbbbb2", MatchScore: 0.4,
		},
	}
}

func newTestServer(t *testing.T, cfg *config.Config, searcher Searcher, dl Downloader) *Server {
	t.Helper()
	if searcher == nil {
		items := sampleItems()
		searcher = &stubSearcher{resp: &search.Response{
			Query: "lemon", LimitRequested: 10, LimitUsed: 10,
			MinScoreUsed: 0, Total: len(items), Truncated: false, Results: items,
		}}
	}
	store := session.NewStore(session.Options{TTL: cfg.SessionTTL})
	if dl == nil {
		path := filepath.Join(cfg.DownloadDir, "3NNhrqHZqlI.mp3")
		data := bytes.Repeat([]byte("ID3fakeaudio"), 100)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		dl = &stubDownloader{result: &download.Result{
			Path: path, Size: int64(len(data)), Cached: false,
			Token: "0123456789abcdef0123456789abcdef", Format: "mp3", Bitrate: "192",
			VideoID: "3NNhrqHZqlI", Title: "Lemon", Artists: []string{"Kenshi Yonezu"},
			DisplayName: "Lemon - Kenshi Yonezu", DurationSeconds: 257,
			ExpiresIn: 3600, ContentType: "audio/mpeg", Filename: "Lemon.mp3",
		}}
	}
	srv, err := New(Options{Config: cfg, Searcher: searcher, Sessions: store, Downloader: dl})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHealthz(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	rr := doJSON(t, srv.Handler(), "GET", "/healthz", nil, nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["status"] != "ok" {
		t.Fatalf("status field: %v", m)
	}
	if int(m["default_limit"].(float64)) != 10 {
		t.Fatalf("default_limit: %v", m["default_limit"])
	}
	if _, ok := m["ytdlp"]; !ok {
		t.Fatalf("ytdlp field missing: %v", m)
	}
}

func TestHealthzReportsYtdlpVersion(t *testing.T) {
	cfg := testCfg(t)
	store := session.NewStore(session.Options{TTL: cfg.SessionTTL})
	items := sampleItems()
	st := &stubSearcher{resp: &search.Response{
		Query: "lemon", LimitRequested: 10, LimitUsed: 10,
		Total: len(items), Results: items,
	}}
	path := filepath.Join(cfg.DownloadDir, "x.mp3")
	_ = os.WriteFile(path, []byte("abc"), 0o644)
	dl := &stubDownloader{result: &download.Result{
		Path: path, Size: 3, Token: "0123456789abcdef0123456789abcdef",
		Format: "mp3", VideoID: "3NNhrqHZqlI", ContentType: "audio/mpeg", Filename: "x.mp3",
	}}
	srv, err := New(Options{
		Config: cfg, Searcher: st, Sessions: store, Downloader: dl,
		YtdlpVersion: "2026.07.04",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := doJSON(t, srv.Handler(), "GET", "/healthz", nil, nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["ytdlp"] != "2026.07.04" {
		t.Fatalf("ytdlp=%v", m["ytdlp"])
	}
}

func TestSearchOK(t *testing.T) {
	cfg := testCfg(t)
	items := sampleItems()
	st := &stubSearcher{resp: &search.Response{
		Query: "lemon", LimitRequested: 10, LimitUsed: 10,
		Total: 2, Truncated: true, Results: items[:2],
	}}
	srv := newTestServer(t, cfg, st, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/search", map[string]any{
		"query": "lemon", "limit": 10, "min_score": 0.35,
	}, nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body SearchResponseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SessionID == "" || !strings.HasPrefix(body.SessionID, "s_") {
		t.Fatalf("session_id: %q", body.SessionID)
	}
	if body.Total != 2 || len(body.Results) != 2 {
		t.Fatalf("total/results: %d/%d", body.Total, len(body.Results))
	}
	if body.Results[0].Index != 1 || body.Results[0].VideoID != "3NNhrqHZqlI" {
		t.Fatalf("first: %+v", body.Results[0])
	}
	if body.ExpiresIn <= 0 {
		t.Fatalf("expires_in: %d", body.ExpiresIn)
	}
	if st.last.Query != "lemon" || st.last.Limit == nil || *st.last.Limit != 10 {
		t.Fatalf("searcher got: %+v", st.last)
	}
	if body.Results[1].Title != "晴天" {
		t.Fatalf("cjk title lost: %+v", body.Results[1])
	}
}

func TestSearchEmptyQuery400(t *testing.T) {
	cfg := testCfg(t)
	st := &stubSearcher{err: search.ErrEmptyQuery}
	srv := newTestServer(t, cfg, st, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/search", map[string]any{"query": "x"}, nil)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "INVALID_REQUEST")
}

func TestSearchInvalidLimit400(t *testing.T) {
	cfg := testCfg(t)
	st := &stubSearcher{err: search.ErrInvalidLimit}
	srv := newTestServer(t, cfg, st, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/search", map[string]any{"query": "a", "limit": 0}, nil)
	if rr.Code != 400 {
		t.Fatalf("status=%d", rr.Code)
	}
	assertCode(t, rr, "INVALID_REQUEST")
}

func TestAPIKeyRequired(t *testing.T) {
	cfg := testCfg(t)
	cfg.APIKey = "secret"
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()
	rr := doJSON(t, h, "GET", "/healthz", nil, nil)
	if rr.Code != 401 {
		t.Fatalf("no key status=%d", rr.Code)
	}
	assertCode(t, rr, "UNAUTHORIZED")

	rr = doJSON(t, h, "GET", "/healthz", nil, map[string]string{"X-API-Key": "wrong"})
	if rr.Code != 401 {
		t.Fatalf("wrong key status=%d", rr.Code)
	}

	rr = doJSON(t, h, "GET", "/healthz", nil, map[string]string{"X-API-Key": "secret"})
	if rr.Code != 200 {
		t.Fatalf("good key status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadBinaryByIndex(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	rr := doJSON(t, h, "POST", "/search", map[string]any{"query": "lemon"}, nil)
	if rr.Code != 200 {
		t.Fatalf("search: %d %s", rr.Code, rr.Body.String())
	}
	var sbody SearchResponseBody
	_ = json.Unmarshal(rr.Body.Bytes(), &sbody)

	rr = doJSON(t, h, "POST", "/download", map[string]any{
		"session_id": sbody.SessionID, "index": 1,
	}, nil)
	if rr.Code != 200 {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "audio/mpeg") {
		t.Fatalf("content-type: %s", ct)
	}
	if rr.Header().Get("X-Track-Video-Id") != "3NNhrqHZqlI" {
		t.Fatalf("video id header: %s", rr.Header().Get("X-Track-Video-Id"))
	}
	if rr.Header().Get("X-Cache") != "miss" {
		t.Fatalf("x-cache: %s", rr.Header().Get("X-Cache"))
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "filename*") {
		t.Fatalf("disposition: %s", rr.Header().Get("Content-Disposition"))
	}
	if rr.Body.Len() == 0 {
		t.Fatal("empty body")
	}
}

func TestDownloadJSONMode(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/download?mode=json", map[string]any{
		"video_id": "3NNhrqHZqlI",
	}, nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body DownloadJSONBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.VideoID != "3NNhrqHZqlI" || body.Format != "mp3" {
		t.Fatalf("body: %+v", body)
	}
	if !strings.HasPrefix(body.FileURL, "/file/") {
		t.Fatalf("file_url: %s", body.FileURL)
	}
	if body.Filesize <= 0 {
		t.Fatalf("filesize: %d", body.Filesize)
	}
}

func TestDownloadByNameAndAmbiguous(t *testing.T) {
	cfg := testCfg(t)
	items := sampleItems()
	st := &stubSearcher{resp: &search.Response{
		Query: "same", LimitRequested: 10, LimitUsed: 10,
		Total: len(items), Results: items,
	}}
	srv := newTestServer(t, cfg, st, nil)
	h := srv.Handler()
	rr := doJSON(t, h, "POST", "/search", map[string]any{"query": "same"}, nil)
	var sbody SearchResponseBody
	_ = json.Unmarshal(rr.Body.Bytes(), &sbody)

	rr = doJSON(t, h, "POST", "/download?mode=json", map[string]any{
		"session_id": sbody.SessionID, "name": "Lemon - Kenshi Yonezu",
	}, nil)
	if rr.Code != 200 {
		t.Fatalf("unique name: %d %s", rr.Code, rr.Body.String())
	}

	// Fuzzy name "Same" matches both "Same - A" and "Same - B".
	rr = doJSON(t, h, "POST", "/download", map[string]any{
		"session_id": sbody.SessionID, "name": "Same",
	}, nil)
	if rr.Code != 409 {
		t.Fatalf("ambiguous want 409 got %d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "AMBIGUOUS_NAME")
	var eb ErrorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &eb)
	if eb.Detail == nil {
		t.Fatal("detail should include candidates")
	}
}

func TestDownloadIndexNotFound404(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()
	rr := doJSON(t, h, "POST", "/search", map[string]any{"query": "lemon"}, nil)
	var sbody SearchResponseBody
	_ = json.Unmarshal(rr.Body.Bytes(), &sbody)
	rr = doJSON(t, h, "POST", "/download", map[string]any{
		"session_id": sbody.SessionID, "index": 99,
	}, nil)
	if rr.Code != 404 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "NOT_FOUND")
}

func TestDownloadSessionExpired410(t *testing.T) {
	cfg := testCfg(t)
	cfg.SessionTTL = time.Second
	now := time.Now()
	store := session.NewStore(session.Options{
		TTL: time.Second,
		Now: func() time.Time { return now },
	})
	items := sampleItems()[:1]
	st := &stubSearcher{resp: &search.Response{
		Query: "lemon", LimitRequested: 10, LimitUsed: 10, Total: 1, Results: items,
	}}
	path := filepath.Join(cfg.DownloadDir, "x.mp3")
	_ = os.WriteFile(path, []byte("abc"), 0o644)
	dl := &stubDownloader{result: &download.Result{
		Path: path, Size: 3, Token: "0123456789abcdef0123456789abcdef",
		Format: "mp3", VideoID: "3NNhrqHZqlI", ContentType: "audio/mpeg", Filename: "x.mp3",
	}}
	srv, err := New(Options{Config: cfg, Searcher: st, Sessions: store, Downloader: dl, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	rr := doJSON(t, h, "POST", "/search", map[string]any{"query": "lemon"}, nil)
	var sbody SearchResponseBody
	_ = json.Unmarshal(rr.Body.Bytes(), &sbody)

	now = now.Add(2 * time.Second)
	rr = doJSON(t, h, "POST", "/download", map[string]any{
		"session_id": sbody.SessionID, "index": 1,
	}, nil)
	if rr.Code != 410 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "SESSION_EXPIRED")
}

func TestDownloadTooLarge413(t *testing.T) {
	cfg := testCfg(t)
	dl := &stubDownloader{err: &download.TooLargeError{Size: 99, MaxBytes: 10, VideoID: "3NNhrqHZqlI", Format: "mp3"}}
	srv := newTestServer(t, cfg, nil, dl)
	rr := doJSON(t, srv.Handler(), "POST", "/download", map[string]any{"video_id": "3NNhrqHZqlI"}, nil)
	if rr.Code != 413 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "FILE_TOO_LARGE")
}

func TestDownloadBadRequest400(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/download", map[string]any{}, nil)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "INVALID_REQUEST")
}

func TestFileTokenAndRange(t *testing.T) {
	cfg := testCfg(t)
	path := filepath.Join(cfg.DownloadDir, "file.mp3")
	data := []byte("0123456789ABCDEFGHIJ") // 20 bytes
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	token := "abcdef0123456789abcdef0123456789"
	dl := &stubDownloader{result: &download.Result{
		Path: path, Size: int64(len(data)), Cached: true, Token: token,
		Format: "mp3", VideoID: "3NNhrqHZqlI", Title: "Lemon",
		Artists: []string{"Kenshi Yonezu"}, ContentType: "audio/mpeg", Filename: "Lemon.mp3",
	}}
	srv := newTestServer(t, cfg, nil, dl)
	h := srv.Handler()

	// full
	req := httptest.NewRequest("GET", "/file/"+token, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("full status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != string(data) {
		t.Fatalf("body mismatch")
	}
	if rr.Header().Get("X-Cache") != "hit" {
		t.Fatalf("x-cache: %s", rr.Header().Get("X-Cache"))
	}

	// range
	req = httptest.NewRequest("GET", "/file/"+token, nil)
	req.Header.Set("Range", "bytes=0-3")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 206 {
		t.Fatalf("range status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "0123" {
		t.Fatalf("range body=%q", rr.Body.String())
	}

	// invalid tokens should be rejected (encoded traversal, non-hex, empty).
	// Note: raw "/file/../etc/passwd" is cleaned by ServeMux into a 307 redirect
	// before our handler runs; the encoded form reaches the handler.
	for _, bad := range []string{"..%2Fetc%2Fpasswd", "not-a-token", "short"} {
		req = httptest.NewRequest("GET", "/file/"+bad, nil)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 400 && rr.Code != 404 {
			t.Fatalf("bad token %q status=%d body=%s", bad, rr.Code, rr.Body.String())
		}
	}
}

func TestMethodNotAllowedAndNotFound(t *testing.T) {
	cfg := testCfg(t)
	h := newTestServer(t, cfg, nil, nil).Handler()
	rr := doJSON(t, h, "POST", "/healthz", map[string]any{}, nil)
	if rr.Code != 405 {
		t.Fatalf("method: %d", rr.Code)
	}
	rr = doJSON(t, h, "GET", "/nope", nil, nil)
	if rr.Code != 404 {
		t.Fatalf("not found: %d", rr.Code)
	}
}

func TestUpstreamError502(t *testing.T) {
	cfg := testCfg(t)
	st := &stubSearcher{err: errors.New("search: upstream: boom")}
	srv := newTestServer(t, cfg, st, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/search", map[string]any{"query": "x"}, nil)
	if rr.Code != 502 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "UPSTREAM_ERROR")
}

func TestBodyTooLarge400(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	// Build a body larger than 1 MiB.
	payload := `{"query":"` + strings.Repeat("a", maxJSONBody+10) + `"}`
	req := httptest.NewRequest("POST", "/search", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "INVALID_REQUEST")
}

func assertCode(t *testing.T, rr *httptest.ResponseRecorder, code string) {
	t.Helper()
	var eb ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &eb); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if eb.Code != code {
		t.Fatalf("code want %s got %s message=%s", code, eb.Code, eb.Message)
	}
	if eb.Message == "" {
		t.Fatal("empty message")
	}
}

func TestErrorMessagesReadable(t *testing.T) {
	cfg := testCfg(t)
	srv := newTestServer(t, cfg, nil, nil)
	h := srv.Handler()

	// empty selection -> INVALID_REQUEST with readable message
	rr := doJSON(t, h, "POST", "/download", map[string]any{}, nil)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var eb ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &eb); err != nil {
		t.Fatal(err)
	}
	if eb.Code != "INVALID_REQUEST" {
		t.Fatalf("code=%s", eb.Code)
	}
	if strings.Contains(eb.Message, "?") {
		t.Fatalf("message looks corrupted: %q", eb.Message)
	}
	if eb.Message == "" {
		t.Fatal("empty message")
	}

	// session expired message
	cfg2 := testCfg(t)
	cfg2.SessionTTL = time.Second
	now := time.Now()
	store := session.NewStore(session.Options{TTL: time.Second, Now: func() time.Time { return now }})
	items := sampleItems()[:1]
	st := &stubSearcher{resp: &search.Response{
		Query: "lemon", LimitRequested: 10, LimitUsed: 10, Total: 1, Results: items,
	}}
	path := filepath.Join(cfg2.DownloadDir, "x.mp3")
	_ = os.WriteFile(path, []byte("abc"), 0o644)
	dl := &stubDownloader{result: &download.Result{
		Path: path, Size: 3, Token: "0123456789abcdef0123456789abcdef",
		Format: "mp3", VideoID: "3NNhrqHZqlI", ContentType: "audio/mpeg", Filename: "x.mp3",
	}}
	srv2, err := New(Options{Config: cfg2, Searcher: st, Sessions: store, Downloader: dl, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	rr = doJSON(t, srv2.Handler(), "POST", "/search", map[string]any{"query": "lemon"}, nil)
	var sbody SearchResponseBody
	_ = json.Unmarshal(rr.Body.Bytes(), &sbody)
	now = now.Add(2 * time.Second)
	rr = doJSON(t, srv2.Handler(), "POST", "/download", map[string]any{
		"session_id": sbody.SessionID, "index": 1,
	}, nil)
	if rr.Code != 410 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &eb)
	if eb.Code != "SESSION_EXPIRED" {
		t.Fatalf("code=%s", eb.Code)
	}
	if strings.Contains(eb.Message, "?") || !strings.Contains(eb.Message, "会话") {
		t.Fatalf("session message corrupted/missing: %q", eb.Message)
	}
}

func TestTimeoutMapsTo504(t *testing.T) {
	cfg := testCfg(t)
	st := &stubSearcher{err: context.DeadlineExceeded}
	srv := newTestServer(t, cfg, st, nil)
	rr := doJSON(t, srv.Handler(), "POST", "/search", map[string]any{"query": "x"}, nil)
	if rr.Code != 504 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCode(t, rr, "TIMEOUT")
	var eb ErrorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &eb)
	if strings.Contains(eb.Message, "?") {
		t.Fatalf("timeout message corrupted: %q", eb.Message)
	}
}
