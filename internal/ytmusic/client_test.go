package ytmusic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSearchResponseLemonFixture(t *testing.T) {
	body := readFixture(t, "search_songs_lemon.json")
	tracks, err := ParseSearchResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tracks) < 10 {
		t.Fatalf("expected >=10 tracks, got %d", len(tracks))
	}
	first := tracks[0]
	if first.VideoID == "" {
		t.Fatalf("first track missing videoId: %+v", first)
	}
	if first.Title == "" {
		t.Fatalf("first track missing title: %+v", first)
	}
	if len(first.Artists) == 0 {
		t.Fatalf("first track missing artists: %+v", first)
	}
	if first.Duration == "" || first.DurationSeconds <= 0 {
		t.Fatalf("first track missing duration: %+v", first)
	}
	if first.Thumbnail == "" {
		t.Fatalf("first track missing thumbnail: %+v", first)
	}
	if first.MusicVideoType != "MUSIC_VIDEO_TYPE_ATV" {
		t.Fatalf("first track musicVideoType=%q want ATV", first.MusicVideoType)
	}
	// Lemon by Kenshi Yonezu should appear first for this fixture.
	if !strings.EqualFold(first.Title, "Lemon") {
		t.Fatalf("unexpected first title %q", first.Title)
	}
	joined := strings.ToLower(strings.Join(first.Artists, " "))
	if !strings.Contains(joined, "kenshi") && !strings.Contains(joined, "yonezu") {
		t.Fatalf("unexpected first artists %v", first.Artists)
	}
	if first.Album == "" {
		t.Fatalf("expected album on first track, got empty")
	}
	if first.DurationSeconds != 4*60+17 {
		t.Fatalf("duration seconds want 257, got %d (%s)", first.DurationSeconds, first.Duration)
	}

	// All tracks must have non-empty videoId/title and non-nil artists slice.
	for i, tr := range tracks {
		if tr.VideoID == "" {
			t.Fatalf("track %d missing videoId", i)
		}
		if tr.Title == "" {
			t.Fatalf("track %d missing title", i)
		}
		if tr.Artists == nil {
			t.Fatalf("track %d artists is nil", i)
		}
	}
}

func TestParseSearchResponseVideosMixedFixture(t *testing.T) {
	body := readAnyFixture(t, "search_videos_mixed.json")
	tracks, err := ParseSearchResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tracks) != 4 {
		t.Fatalf("tracks=%d want 4", len(tracks))
	}

	byID := map[string]Track{}
	for _, tr := range tracks {
		byID[tr.VideoID] = tr
	}

	omv := byID["omvLemon1"]
	if omv.Title != "Lemon" {
		t.Fatalf("omv title=%q", omv.Title)
	}
	if omv.MusicVideoType != "MUSIC_VIDEO_TYPE_OMV" {
		t.Fatalf("omv type=%q", omv.MusicVideoType)
	}
	if len(omv.Artists) == 0 || !strings.Contains(strings.ToLower(omv.Artists[0]), "kenshi") {
		t.Fatalf("omv artists=%v", omv.Artists)
	}
	if omv.DurationSeconds != 4*60+16 {
		t.Fatalf("omv duration=%d", omv.DurationSeconds)
	}

	ugc := byID["ugcCover1"]
	if ugc.MusicVideoType != "MUSIC_VIDEO_TYPE_UGC" {
		t.Fatalf("ugc type=%q", ugc.MusicVideoType)
	}

	atv := byID["atvSong1"]
	if atv.MusicVideoType != "MUSIC_VIDEO_TYPE_ATV" {
		t.Fatalf("atv type=%q", atv.MusicVideoType)
	}

	menuOnly := byID["menuOnlyOMV"]
	if menuOnly.MusicVideoType != "MUSIC_VIDEO_TYPE_OMV" {
		t.Fatalf("menu-only type=%q want OMV from menu watchEndpoint", menuOnly.MusicVideoType)
	}
}

func TestSearchFilterParams(t *testing.T) {
	if SearchFilterSongs.params() != songsFilterParams {
		t.Fatalf("songs params=%q", SearchFilterSongs.params())
	}
	if SearchFilterVideos.params() != videosFilterParams {
		t.Fatalf("videos params=%q", SearchFilterVideos.params())
	}
	if SearchFilter("").params() != songsFilterParams {
		t.Fatalf("empty filter should default to songs")
	}
	if SearchFilter("nope").params() != songsFilterParams {
		t.Fatalf("unknown filter should default to songs")
	}
}

func TestParseSearchResponseMissingFieldsNoPanic(t *testing.T) {
	cases := []string{
		`{}`,
		`{"contents":{}}`,
		`{"contents":{"tabbedSearchResultsRenderer":{"tabs":[]}}}`,
		`{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"contents":[
			{"musicResponsiveListItemRenderer":{}},
			{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"abc"},"flexColumns":[]}},
			{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"def"},"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Only Title"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Artist"},{"text":" • "},{"text":"3:21"}]}}}
			]}}
		]}}]}}}}]}}}`,
	}
	for i, raw := range cases {
		tracks, err := ParseSearchResponse([]byte(raw))
		if err != nil {
			t.Fatalf("case %d unexpected err: %v", i, err)
		}
		// first three cases empty; last case has two items (one empty renderer skipped, one partial, one full-ish)
		_ = tracks
	}

	// Explicit partial item assertions.
	body := []byte(`{"x":[{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"vid1"},"flexColumns":[
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"T"}]}}},
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"A"},{"text":" • "},{"text":"1:02"}]}}}
	]}},{"musicResponsiveListItemRenderer":{"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"NoVideo"}]}}}]}}]}`)
	tracks, err := ParseSearchResponse(body)
	if err != nil {
		t.Fatalf("partial parse: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("want 1 track (skip no-video), got %d", len(tracks))
	}
	if tracks[0].VideoID != "vid1" || tracks[0].Title != "T" {
		t.Fatalf("unexpected track %+v", tracks[0])
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "A" {
		t.Fatalf("unexpected artists %+v", tracks[0].Artists)
	}
	if tracks[0].Duration != "1:02" || tracks[0].DurationSeconds != 62 {
		t.Fatalf("unexpected duration %+v", tracks[0])
	}
}

func TestParseSearchResponseInvalidJSON(t *testing.T) {
	if _, err := ParseSearchResponse([]byte(`{not-json`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
	if _, err := ParseSearchResponse(nil); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestParseDurationSeconds(t *testing.T) {
	cases := map[string]int{
		"":         0,
		"bad":      0,
		"0:05":     5,
		"1:02":     62,
		"4:17":     257,
		"1:02:03":  3723,
		"10:00:00": 36000,
	}
	for in, want := range cases {
		if got := parseDurationSeconds(in); got != want {
			t.Fatalf("parseDurationSeconds(%q)=%d want %d", in, got, want)
		}
	}
}

func TestSearchUsesHTTPClientAndParses(t *testing.T) {
	body := readFixture(t, "search_songs_lemon.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if r.URL.Query().Get("key") == "" {
			t.Errorf("missing key query")
		}
		if r.Header.Get("Origin") != "https://music.youtube.com" {
			t.Errorf("origin %q", r.Header.Get("Origin"))
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("content-type %q", ct)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload["query"] != "lemon kenshi yonezu" {
			t.Errorf("query=%v", payload["query"])
		}
		if payload["params"] != songsFilterParams {
			t.Errorf("params=%v", payload["params"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	client, err := New(Options{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tracks, err := client.Search(context.Background(), "lemon kenshi yonezu")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tracks) < 10 {
		t.Fatalf("tracks=%d", len(tracks))
	}
	if tracks[0].VideoID == "" || tracks[0].Title == "" {
		t.Fatalf("bad first track %+v", tracks[0])
	}
}

func TestSearchFilterVideosUsesVideosParams(t *testing.T) {
	body := readAnyFixture(t, "search_videos_mixed.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload["query"] != "lemon official" {
			t.Errorf("query=%v", payload["query"])
		}
		if payload["params"] != videosFilterParams {
			t.Errorf("params=%v want %s", payload["params"], videosFilterParams)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	client, err := New(Options{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tracks, err := client.SearchFilter(context.Background(), "lemon official", SearchFilterVideos)
	if err != nil {
		t.Fatalf("SearchFilter: %v", err)
	}
	if len(tracks) != 4 {
		t.Fatalf("tracks=%d", len(tracks))
	}
	if tracks[0].MusicVideoType != "MUSIC_VIDEO_TYPE_OMV" {
		t.Fatalf("first type=%q", tracks[0].MusicVideoType)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	client, err := New(Options{HTTPClient: &http.Client{Timeout: time.Second}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Search(context.Background(), "  "); err == nil {
		t.Fatal("expected empty query error")
	}
}

func TestSearchUpstreamStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	client, err := New(Options{BaseURL: srv.URL, HTTPClient: srv.Client(), APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Search(context.Background(), "x"); err == nil {
		t.Fatal("expected status error")
	}
}

func TestNewInvalidProxy(t *testing.T) {
	if _, err := New(Options{Proxy: "://bad"}); err == nil {
		t.Fatal("expected invalid proxy error")
	}
}

func TestClientVersionFormat(t *testing.T) {
	ts := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	got := clientVersion(ts)
	if got != "1.20260726.01.00" {
		t.Fatalf("clientVersion=%q", got)
	}
}

func TestLiveSearchLemon(t *testing.T) {
	if os.Getenv("YTM_LIVE") == "" && !testing.Short() {
		// still allow -tags style opt-in via env; default skip in unit runs is NOT desired by task:
		// task requires live verification. We always try live unless explicitly disabled.
	}
	if os.Getenv("YTM_SKIP_LIVE") == "1" {
		t.Skip("YTM_SKIP_LIVE=1")
	}
	client, err := New(Options{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	tracks, err := client.Search(ctx, "lemon kenshi yonezu")
	if err != nil {
		t.Fatalf("live search: %v", err)
	}
	if len(tracks) < 10 {
		t.Fatalf("live tracks=%d want >=10", len(tracks))
	}
	first := tracks[0]
	if first.VideoID == "" || first.Title == "" || len(first.Artists) == 0 || first.Duration == "" {
		t.Fatalf("live first incomplete: %+v", first)
	}
	t.Logf("live first: %s | %s | %v | %s", first.VideoID, first.Title, first.Artists, first.Duration)
}

func TestLiveSearchChinese(t *testing.T) {
	if os.Getenv("YTM_SKIP_LIVE") == "1" {
		t.Skip("YTM_SKIP_LIVE=1")
	}
	client, err := New(Options{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	tracks, err := client.Search(ctx, "晴天 周杰伦")
	if err != nil {
		t.Fatalf("live zh search: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("live zh returned 0 tracks")
	}
	if tracks[0].VideoID == "" || tracks[0].Title == "" {
		t.Fatalf("live zh first incomplete: %+v", tracks[0])
	}
	t.Logf("live zh first: %s | %s | %v", tracks[0].VideoID, tracks[0].Title, tracks[0].Artists)
}

func TestLiveSearchJapanese(t *testing.T) {
	if os.Getenv("YTM_SKIP_LIVE") == "1" {
		t.Skip("YTM_SKIP_LIVE=1")
	}
	client, err := New(Options{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	tracks, err := client.Search(ctx, "レモン 米津玄師")
	if err != nil {
		t.Fatalf("live ja search: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("live ja returned 0 tracks")
	}
	if tracks[0].VideoID == "" || tracks[0].Title == "" {
		t.Fatalf("live ja first incomplete: %+v", tracks[0])
	}
	t.Logf("live ja first: %s | %s | %v", tracks[0].VideoID, tracks[0].Title, tracks[0].Artists)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if len(body) < 1000 {
		t.Fatalf("fixture %s too small: %d bytes", path, len(body))
	}
	return body
}

func readAnyFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if len(body) == 0 {
		t.Fatalf("fixture %s empty", path)
	}
	return body
}

func TestParseSearchResponseNonObjectRoot(t *testing.T) {
	cases := []string{`[]`, `null`, `123`, `"str"`, `true`}
	for _, raw := range cases {
		tracks, err := ParseSearchResponse([]byte(raw))
		if err != nil {
			// null/number/bool/string are valid JSON; walk should yield empty without error
			// only invalid JSON errors. These are valid.
			t.Fatalf("raw=%s err=%v", raw, err)
		}
		if len(tracks) != 0 {
			t.Fatalf("raw=%s tracks=%d", raw, len(tracks))
		}
	}
}

func TestParseSearchResponseSkipsNonSongRenderers(t *testing.T) {
	body := []byte(`{"a":[{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"ok1"},"flexColumns":[
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Title"}]}}},
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Artist"},{"text":" ? "},{"text":"Album"},{"text":" ? "},{"text":"2:00"}]}}}
	]}},{"somethingElse":{}},{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":""}}}]}`)
	tracks, err := ParseSearchResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].VideoID != "ok1" {
		t.Fatalf("%+v", tracks)
	}
	if tracks[0].DurationSeconds != 120 {
		t.Fatalf("duration=%d", tracks[0].DurationSeconds)
	}
}

func TestSearchContextCancel(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	httpClient := srv.Client()
	httpClient.Timeout = 0
	client, err := New(Options{BaseURL: srv.URL, HTTPClient: httpClient, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, "x")
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see request")
	}
	cancel()
	select {
	case err = <-errCh:
		if err == nil {
			t.Fatal("expected cancel error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after cancel")
	}
}
