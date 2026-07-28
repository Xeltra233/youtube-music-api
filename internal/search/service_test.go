package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

type stubUpstream struct {
	tracks []ytmusic.Track
	err    error
	calls  int
	lastQ  string
}

func (s *stubUpstream) Search(_ context.Context, query string) ([]ytmusic.Track, error) {
	s.calls++
	s.lastQ = query
	if s.err != nil {
		return nil, s.err
	}
	// ????? Artists?????????????????
	out := make([]ytmusic.Track, len(s.tracks))
	for i, tr := range s.tracks {
		out[i] = tr
		if tr.Artists != nil {
			out[i].Artists = append([]string(nil), tr.Artists...)
		}
	}
	return out, nil
}

// stubVideoUpstream 同时实现 songs Search 与 videos SearchFilter。
type stubVideoUpstream struct {
	songs      []ytmusic.Track
	videos     []ytmusic.Track
	songsErr   error
	videosErr  error
	songCalls  int
	videoCalls int
	lastSongQ  string
	lastVideoQ string
	lastFilter ytmusic.SearchFilter
}

func (s *stubVideoUpstream) Search(_ context.Context, query string) ([]ytmusic.Track, error) {
	s.songCalls++
	s.lastSongQ = query
	if s.songsErr != nil {
		return nil, s.songsErr
	}
	return cloneTracks(s.songs), nil
}

func (s *stubVideoUpstream) SearchFilter(_ context.Context, query string, filter ytmusic.SearchFilter) ([]ytmusic.Track, error) {
	s.videoCalls++
	s.lastVideoQ = query
	s.lastFilter = filter
	if s.videosErr != nil {
		return nil, s.videosErr
	}
	return cloneTracks(s.videos), nil
}

func cloneTracks(in []ytmusic.Track) []ytmusic.Track {
	out := make([]ytmusic.Track, len(in))
	for i, tr := range in {
		out[i] = tr
		if tr.Artists != nil {
			out[i].Artists = append([]string(nil), tr.Artists...)
		}
	}
	return out
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	// 固定本测期望值，避免环境变量干扰。
	cfg.DefaultLimit = 10
	cfg.MaxLimit = 20
	cfg.MinScore = 0.0
	return cfg
}

func track(id, title string, artists []string) ytmusic.Track {
	return ytmusic.Track{
		VideoID:         id,
		Title:           title,
		Artists:         artists,
		Album:           "Album",
		Duration:        "3:00",
		DurationSeconds: 180,
		Thumbnail:       "http://img/" + id,
	}
}

func videoTrack(id, title string, artists []string, musicType string) ytmusic.Track {
	tr := track(id, title, artists)
	tr.MusicVideoType = musicType
	return tr
}

func makeTracks(n int) []ytmusic.Track {
	out := make([]ytmusic.Track, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, track(
			fmt.Sprintf("vid%02d", i),
			fmt.Sprintf("Song %02d", i),
			[]string{fmt.Sprintf("Artist %02d", i)},
		))
	}
	return out
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func TestSearchDefaultLimit(t *testing.T) {
	up := &stubUpstream{tracks: makeTracks(20)}
	svc, err := New(up, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Search(context.Background(), Request{Query: "Song 01"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.LimitRequested != 10 || resp.LimitUsed != 10 {
		t.Fatalf("limit requested/used=%d/%d want 10/10", resp.LimitRequested, resp.LimitUsed)
	}
	if resp.Total != 10 {
		t.Fatalf("total=%d want 10", resp.Total)
	}
	if !resp.Truncated {
		t.Fatal("expected truncated=true for 20->10")
	}
	if len(resp.Results) != 10 {
		t.Fatalf("results=%d", len(resp.Results))
	}
	// index 1-based continuous
	for i, item := range resp.Results {
		if item.Index != i+1 {
			t.Fatalf("index[%d]=%d", i, item.Index)
		}
		if item.DisplayName == "" || item.VideoID == "" {
			t.Fatalf("item incomplete: %+v", item)
		}
		if item.Artists == nil {
			t.Fatalf("artists nil at %d", i)
		}
	}
}

func TestSearchClampLimit25To20(t *testing.T) {
	up := &stubUpstream{tracks: makeTracks(20)}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Song", Limit: intPtr(25)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.LimitRequested != 25 {
		t.Fatalf("limit_requested=%d want 25", resp.LimitRequested)
	}
	if resp.LimitUsed != 20 {
		t.Fatalf("limit_used=%d want 20", resp.LimitUsed)
	}
	if resp.Total != 20 || resp.Truncated {
		// 过滤后刚好 20，没有更多可截，truncated=false
		// 上游 20，limit_used 20，截断后 total=20，truncated 应 false
	}
	if resp.Total != 20 {
		t.Fatalf("total=%d want 20", resp.Total)
	}
	if resp.Truncated {
		t.Fatal("truncated should be false when filtered count == limit_used")
	}
}

func TestSearchLimitZeroError(t *testing.T) {
	svc, _ := New(&stubUpstream{tracks: makeTracks(3)}, testConfig(t))
	_, err := svc.Search(context.Background(), Request{Query: "x", Limit: intPtr(0)})
	if !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("err=%v want ErrInvalidLimit", err)
	}
	_, err = svc.Search(context.Background(), Request{Query: "x", Limit: intPtr(-3)})
	if !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("err=%v want ErrInvalidLimit", err)
	}
}

func TestSearchR6FewerThanLimit(t *testing.T) {
	up := &stubUpstream{tracks: makeTracks(3)}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Song", Limit: intPtr(10)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 {
		t.Fatalf("total=%d want 3", resp.Total)
	}
	if resp.Truncated {
		t.Fatal("truncated should be false when only 3 available")
	}
	if resp.LimitUsed != 10 {
		t.Fatalf("limit_used=%d want 10", resp.LimitUsed)
	}
}

func TestSearchMinScoreFiltersAndReindexes(t *testing.T) {
	// 构造：一条高相关 + 若干无关。
	tracks := []ytmusic.Track{
		track("hi", "Lemon", []string{"Kenshi Yonezu"}),
		track("a", "Bohemian Rhapsody", []string{"Queen"}),
		track("b", "Shape of You", []string{"Ed Sheeran"}),
		track("c", "Random Noise", []string{"Nobody"}),
		track("d", "Another Filler", []string{"X"}),
	}
	up := &stubUpstream{tracks: tracks}
	svc, _ := New(up, testConfig(t))

	// 不过滤应返回 5 条（limit 默认 10）
	all, err := svc.Search(context.Background(), Request{Query: "lemon kenshi yonezu"})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 5 {
		t.Fatalf("unfiltered total=%d want 5", all.Total)
	}

	resp, err := svc.Search(context.Background(), Request{
		Query:    "lemon kenshi yonezu",
		MinScore: floatPtr(0.9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.MinScoreUsed != 0.9 {
		t.Fatalf("min_score_used=%v", resp.MinScoreUsed)
	}
	if resp.Total == 0 || resp.Total >= all.Total {
		t.Fatalf("filtered total=%d unfiltered=%d", resp.Total, all.Total)
	}
	// 高相关 lemon 必须留下，且 index 从 1 连续
	for i, item := range resp.Results {
		if item.Index != i+1 {
			t.Fatalf("reindex broken: %+v", item)
		}
		if item.MatchScore < 0.9 {
			t.Fatalf("score below threshold: %+v", item)
		}
	}
	if !strings.Contains(strings.ToLower(resp.Results[0].DisplayName), "lemon") {
		t.Fatalf("expected lemon first, got %+v", resp.Results[0])
	}
	if resp.Truncated {
		// 过滤后条数通常 < limit，不应 truncated
		t.Fatalf("unexpected truncated with total=%d", resp.Total)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	svc, _ := New(&stubUpstream{}, testConfig(t))
	_, err := svc.Search(context.Background(), Request{Query: "   "})
	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("err=%v", err)
	}
}

func TestSearchUpstreamError(t *testing.T) {
	up := &stubUpstream{err: errors.New("boom")}
	svc, _ := New(up, testConfig(t))
	_, err := svc.Search(context.Background(), Request{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
}

func TestSearchSortsByScoreDescending(t *testing.T) {
	tracks := []ytmusic.Track{
		track("low", "Totally Unrelated Track Name ZZZ", []string{"Nobody"}),
		track("high", "Lemon", []string{"Kenshi Yonezu"}),
		track("mid", "Lemon Tree", []string{"Fools Garden"}),
	}
	svc, _ := New(&stubUpstream{tracks: tracks}, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "lemon kenshi yonezu"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) < 2 {
		t.Fatalf("results=%d", len(resp.Results))
	}
	for i := 1; i < len(resp.Results); i++ {
		if resp.Results[i-1].MatchScore < resp.Results[i].MatchScore {
			t.Fatalf("not sorted: %+v", resp.Results)
		}
	}
	if resp.Results[0].VideoID != "high" {
		t.Fatalf("expected high first, got %+v", resp.Results[0])
	}
}

func TestSearchTruncatedTrueWhenMoreThanLimitAfterFilter(t *testing.T) {
	// 20 条都叫 Song，query Song，分数都高；limit=10 应 truncated
	svc, _ := New(&stubUpstream{tracks: makeTracks(20)}, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Song", Limit: intPtr(10)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 10 || !resp.Truncated {
		t.Fatalf("total=%d truncated=%v", resp.Total, resp.Truncated)
	}
}

func TestNewNilArgs(t *testing.T) {
	if _, err := New(nil, testConfig(t)); err == nil {
		t.Fatal("expected nil upstream error")
	}
	if _, err := New(&stubUpstream{}, nil); err == nil {
		t.Fatal("expected nil config error")
	}
}

func TestSearchNilArtistsBecomesEmptySlice(t *testing.T) {
	tr := track("v", "Only Title", nil)
	tr.Artists = nil
	svc, _ := New(&stubUpstream{tracks: []ytmusic.Track{tr}}, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Only Title"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("total=%d", len(resp.Results))
	}
	if resp.Results[0].Artists == nil {
		t.Fatal("artists should be empty slice not nil")
	}
	if resp.Results[0].DisplayName != "Only Title" {
		t.Fatalf("display=%q", resp.Results[0].DisplayName)
	}
}

func TestSearchQueryTrimmed(t *testing.T) {
	up := &stubUpstream{tracks: makeTracks(1)}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "  Song 01  "})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Query != "Song 01" {
		t.Fatalf("query=%q", resp.Query)
	}
	if up.lastQ != "Song 01" {
		t.Fatalf("upstream query=%q", up.lastQ)
	}
}

func TestSearchResultsDefensiveCopy(t *testing.T) {
	artists := []string{"Orig"}
	up := &stubUpstream{tracks: []ytmusic.Track{track("v", "T", artists)}}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "T"})
	if err != nil {
		t.Fatal(err)
	}
	// ??????????????
	resp.Results[0].Artists[0] = "MUT"
	resp2, err := svc.Search(context.Background(), Request{Query: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Results[0].Artists[0] != "Orig" {
		t.Fatalf("artists leaked via response: %v", resp2.Results[0].Artists)
	}
	// ???????????????????????
	artists[0] = "MUT2"
	if resp2.Results[0].Artists[0] != "Orig" {
		t.Fatalf("artists leaked via caller slice: %v", resp2.Results[0].Artists)
	}
}

func TestSearchStableOrderOnEqualScores(t *testing.T) {
	// identical titles => equal scores; stable sort keeps upstream order.
	tracks := []ytmusic.Track{
		track("a", "Same", []string{"X"}),
		track("b", "Same", []string{"X"}),
		track("c", "Same", []string{"X"}),
	}
	svc, _ := New(&stubUpstream{tracks: tracks}, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Same - X"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("n=%d", len(resp.Results))
	}
	got := []string{resp.Results[0].VideoID, resp.Results[1].VideoID, resp.Results[2].VideoID}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v want %v", got, want)
		}
	}
}

func TestSearchAttachesOfficialVideoFromOMV(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("songLemon", "Lemon", []string{"Kenshi Yonezu"}),
			track("songOther", "Shape of You", []string{"Ed Sheeran"}),
		},
		videos: []ytmusic.Track{
			videoTrack("omvLemon", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_OMV"),
			videoTrack("omvShape", "Shape of You", []string{"Ed Sheeran"}, "MUSIC_VIDEO_TYPE_OMV"),
			videoTrack("ugcNoise", "Lemon piano cover", []string{"Random"}, "MUSIC_VIDEO_TYPE_UGC"),
		},
	}
	svc, err := New(up, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Search(context.Background(), Request{Query: "lemon kenshi yonezu", Limit: intPtr(5)})
	if err != nil {
		t.Fatal(err)
	}
	if up.songCalls != 1 || up.videoCalls != 1 {
		t.Fatalf("calls song=%d video=%d", up.songCalls, up.videoCalls)
	}
	if up.lastFilter != ytmusic.SearchFilterVideos {
		t.Fatalf("filter=%q", up.lastFilter)
	}
	if up.lastSongQ != up.lastVideoQ || up.lastSongQ == "" {
		t.Fatalf("queries song=%q video=%q", up.lastSongQ, up.lastVideoQ)
	}

	bySong := map[string]Item{}
	for _, it := range resp.Results {
		bySong[it.VideoID] = it
	}
	lemon := bySong["songLemon"]
	if !lemon.HasOfficialVideo || lemon.OfficialVideoID != "omvLemon" {
		t.Fatalf("lemon official=%+v", lemon)
	}
	if lemon.OfficialVideoURL != "https://www.youtube.com/watch?v=omvLemon" {
		t.Fatalf("lemon url=%q", lemon.OfficialVideoURL)
	}
	// Shape 也可能因 query 分数较低被 min_score=0 保留；若在结果中也应绑上自己的 OMV。
	if shape, ok := bySong["songOther"]; ok {
		if !shape.HasOfficialVideo || shape.OfficialVideoID != "omvShape" {
			t.Fatalf("shape official=%+v", shape)
		}
	}
}

func TestSearchOfficialVideoPrefersOMVOverUGC(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("song1", "Lemon", []string{"Kenshi Yonezu"}),
		},
		videos: []ytmusic.Track{
			videoTrack("ugc1", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_UGC"),
			videoTrack("omv1", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_OMV"),
		},
	}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("n=%d", len(resp.Results))
	}
	if resp.Results[0].OfficialVideoID != "omv1" {
		t.Fatalf("want omv1, got %+v", resp.Results[0])
	}
}

func TestSearchOfficialVideoFallbackToNonATVWhenNoOMV(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("song1", "Lemon", []string{"Kenshi Yonezu"}),
		},
		videos: []ytmusic.Track{
			videoTrack("atv1", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_ATV"),
			videoTrack("ugc1", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_UGC"),
		},
	}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon - Kenshi Yonezu"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].OfficialVideoID != "ugc1" {
		t.Fatalf("want ugc fallback, got %+v", resp.Results[0])
	}
}

func TestSearchOfficialVideoNoMatchBelowThreshold(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("song1", "Lemon", []string{"Kenshi Yonezu"}),
		},
		videos: []ytmusic.Track{
			videoTrack("omvX", "Bohemian Rhapsody", []string{"Queen"}, "MUSIC_VIDEO_TYPE_OMV"),
		},
	}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon"})
	if err != nil {
		t.Fatal(err)
	}
	it := resp.Results[0]
	if it.HasOfficialVideo || it.OfficialVideoID != "" || it.OfficialVideoURL != "" {
		t.Fatalf("expected empty official fields, got %+v", it)
	}
}

func TestSearchVideosUpstreamErrorDegrades(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("song1", "Lemon", []string{"Kenshi Yonezu"}),
		},
		videosErr: errors.New("videos boom"),
	}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon"})
	if err != nil {
		t.Fatalf("songs search should succeed on videos failure: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].VideoID != "song1" {
		t.Fatalf("results=%+v", resp.Results)
	}
	if resp.Results[0].HasOfficialVideo || resp.Results[0].OfficialVideoID != "" {
		t.Fatalf("official should be empty on degrade: %+v", resp.Results[0])
	}
	if up.videoCalls != 1 {
		t.Fatalf("videoCalls=%d", up.videoCalls)
	}
}

func TestSearchWithoutVideoUpstreamLeavesOfficialEmpty(t *testing.T) {
	// 旧 stub 只实现 Search：官方字段应稳定为零值，且不 panic。
	up := &stubUpstream{tracks: []ytmusic.Track{track("song1", "Lemon", []string{"Kenshi Yonezu"})}}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].HasOfficialVideo || resp.Results[0].OfficialVideoID != "" {
		t.Fatalf("unexpected official: %+v", resp.Results[0])
	}
}

func TestSearchSkipsOfficialWhenSameAsSongVideoID(t *testing.T) {
	up := &stubVideoUpstream{
		songs: []ytmusic.Track{
			track("sameID", "Lemon", []string{"Kenshi Yonezu"}),
		},
		videos: []ytmusic.Track{
			videoTrack("sameID", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_OMV"),
		},
	}
	svc, _ := New(up, testConfig(t))
	resp, err := svc.Search(context.Background(), Request{Query: "Lemon"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].HasOfficialVideo {
		t.Fatalf("same id should not count as extra official video: %+v", resp.Results[0])
	}
}

func TestAttachOfficialVideosUnit(t *testing.T) {
	items := []Item{
		{VideoID: "s1", Title: "Lemon", Artists: []string{"Kenshi Yonezu"}, DisplayName: "Lemon - Kenshi Yonezu"},
		{VideoID: "s2", Title: "Unrelated", Artists: []string{"ZZZ"}, DisplayName: "Unrelated - ZZZ"},
	}
	videos := []ytmusic.Track{
		videoTrack("omv1", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_OMV"),
	}
	attachOfficialVideos(items, videos)
	if !items[0].HasOfficialVideo || items[0].OfficialVideoID != "omv1" {
		t.Fatalf("item0=%+v", items[0])
	}
	if items[1].HasOfficialVideo {
		t.Fatalf("item1 should stay empty: %+v", items[1])
	}
}

func TestAttachOfficialVideosRejectsSameArtistDifferentTitle(t *testing.T) {
	items := []Item{
		{VideoID: "ladySong", Title: "LADY", Artists: []string{"Kenshi Yonezu"}, DisplayName: "LADY - Kenshi Yonezu"},
		{VideoID: "lemonSong", Title: "Lemon", Artists: []string{"Kenshi Yonezu"}, DisplayName: "Lemon - Kenshi Yonezu"},
	}
	videos := []ytmusic.Track{
		videoTrack("omvLemon", "Lemon", []string{"Kenshi Yonezu"}, "MUSIC_VIDEO_TYPE_OMV"),
	}
	attachOfficialVideos(items, videos)
	if items[0].HasOfficialVideo {
		t.Fatalf("LADY must not bind Lemon OMV via artist overlap: %+v", items[0])
	}
	if !items[1].HasOfficialVideo || items[1].OfficialVideoID != "omvLemon" {
		t.Fatalf("Lemon should still bind: %+v", items[1])
	}
}
