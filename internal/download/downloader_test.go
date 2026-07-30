package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DownloadDir:            dir,
		AudioFormat:            "mp3",
		AudioBitrate:           "192",
		YtdlpPath:              filepath.Join(dir, "fake-ytdlp"),
		FFmpegLocation:         "",
		MaxConcurrentDownloads: 2,
		MaxFilesizeMB:          50,
		DownloadTimeout:        30 * time.Second,
		CacheTTL:               time.Hour,
		CacheMaxTotalMB:        100,
	}
	return cfg
}

// fakeRunner 模拟 yt-dlp：根据 args 写一个输出文件。
type fakeRunner struct {
	mu        sync.Mutex
	calls     int
	delay     time.Duration
	writeSize int
	fail      bool
	failMsg   string
	// failTimes > 0 时：前 N 次失败，之后成功（用于重试测试）。
	failTimes int
	// onCall 可选钩子
	onCall func(name string, args []string)
}

type probeResult struct {
	stdout string
	stderr string
	err    error
}

type fakeProbeRunner struct {
	mu      sync.Mutex
	calls   int
	results []probeResult
	names   []string
	args    [][]string
}

func (f *fakeProbeRunner) Run(ctx context.Context, name string, args []string, env []string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.names = append(f.names, name)
	f.args = append(f.args, append([]string(nil), args...))
	if len(f.results) == 0 {
		return "video,1920,1080\n", "", nil
	}
	i := f.calls - 1
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	r := f.results[i]
	return r.stdout, r.stderr, r.err
}

func (f *fakeProbeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeProbeRunner) lastCall() (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.names) == 0 {
		return "", nil
	}
	return f.names[len(f.names)-1], append([]string(nil), f.args[len(f.args)-1]...)
}

func configureFakeMediaTools(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := filepath.Join(cfg.DownloadDir, "media-tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg.FFmpegLocation = dir
	return filepath.Join(dir, "ffprobe.exe")
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, env []string) (string, string, error) {
	f.mu.Lock()
	f.calls++
	calls := f.calls
	delay := f.delay
	size := f.writeSize
	fail := f.fail
	failMsg := f.failMsg
	failTimes := f.failTimes
	onCall := f.onCall
	f.mu.Unlock()

	if onCall != nil {
		onCall(name, args)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return "", "canceled", ctx.Err()
		case <-time.After(delay):
		}
	}
	if fail || (failTimes > 0 && calls <= failTimes) {
		msg := failMsg
		if msg == "" {
			msg = "fake ytdlp failed"
		}
		return "", msg, &exec.ExitError{}
	}
	if size <= 0 {
		size = 1024
	}
	out := outputFromArgs(args)
	if out == "" {
		return "", "no output template", fmt.Errorf("no output")
	}
	// 处理 %(ext)s
	path := out
	if strings.Contains(path, "%(ext)s") {
		format := "mp3"
		for i, a := range args {
			if a == "--audio-format" && i+1 < len(args) {
				format = args[i+1]
			} else if a == "--merge-output-format" && i+1 < len(args) {
				format = args[i+1]
			}
		}
		path = strings.ReplaceAll(path, "%(ext)s", format)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err.Error(), err
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('A' + (i % 26))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err.Error(), err
	}
	return fmt.Sprintf("ok call=%d path=%s", calls, path), "", nil
}

func outputFromArgs(args []string) string {
	for i, a := range args {
		if a == "--output" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestValidateVideoID(t *testing.T) {
	cases := []struct {
		id string
		ok bool
	}{
		{"SJKoWAd5ySo", true},
		{"3NNhrqHZqlI", true},
		{"abc123", true},
		{"", false},
		{"../etc/passwd", false},
		{`..\windows`, false},
		{"a/b", false},
		{"short", false}, // 5 chars < 6
		{"this_id_is_way_too_long_for_youtube", false},
		{"bad id!", false},
	}
	for _, tc := range cases {
		err := ValidateVideoID(tc.id)
		if tc.ok && err != nil {
			t.Fatalf("ValidateVideoID(%q) unexpected err: %v", tc.id, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("ValidateVideoID(%q) want error", tc.id)
		}
		if !tc.ok && !errors.Is(err, ErrBadRequest) {
			t.Fatalf("ValidateVideoID(%q) want ErrBadRequest, got %v", tc.id, err)
		}
	}
}

func TestNormalizeFormat(t *testing.T) {
	f, err := NormalizeFormat("", "mp3")
	if err != nil || f != "mp3" {
		t.Fatalf("default mp3: got %q %v", f, err)
	}
	f, err = NormalizeFormat("M4A", "mp3")
	if err != nil || f != "m4a" {
		t.Fatalf("m4a: got %q %v", f, err)
	}
	f, err = NormalizeFormat("MP4", "mp3")
	if err != nil || f != "mp4" {
		t.Fatalf("mp4: got %q %v", f, err)
	}
	_, err = NormalizeFormat("wav", "mp3")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want bad request, got %v", err)
	}
	if !IsVideoFormat("mp4") || IsVideoFormat("mp3") {
		t.Fatal("IsVideoFormat mismatch")
	}
	if !IsAudioFormat("opus") || IsAudioFormat("mp4") {
		t.Fatal("IsAudioFormat mismatch")
	}
}

func TestBuildYtdlpArgsVideoVsAudio(t *testing.T) {
	audio := buildYtdlpArgs(YtdlpOptions{
		Format: "mp3", Bitrate: "192", OutputPath: "x.%(ext)s", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa",
	})
	joinedAudio := strings.Join(audio, " ")
	if !strings.Contains(joinedAudio, "--extract-audio") {
		t.Fatalf("audio args missing extract-audio: %v", audio)
	}
	if strings.Contains(joinedAudio, "--merge-output-format") {
		t.Fatalf("audio args should not merge video: %v", audio)
	}
	if !strings.Contains(joinedAudio, "ba/bestaudio/best") {
		t.Fatalf("audio selector missing: %v", audio)
	}

	video := buildYtdlpArgs(YtdlpOptions{
		Format: "mp4", Bitrate: "0", OutputPath: "y.%(ext)s", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb",
	})
	joinedVideo := strings.Join(video, " ")
	if strings.Contains(joinedVideo, "--extract-audio") {
		t.Fatalf("video args must not extract-audio: %v", video)
	}
	if !strings.Contains(joinedVideo, "--merge-output-format") || !strings.Contains(joinedVideo, "mp4") {
		t.Fatalf("video args missing merge mp4: %v", video)
	}
	if !strings.Contains(joinedVideo, "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/bv*+ba/b") {
		t.Fatalf("video selector missing: %v", video)
	}
	// yt-dlp >= 2026.07 hard-fails on deprecated --prefer-ffmpeg.
	if strings.Contains(joinedAudio, "--prefer-ffmpeg") || strings.Contains(joinedVideo, "--prefer-ffmpeg") {
		t.Fatal("args must not include deprecated --prefer-ffmpeg")
	}
}

func TestDownloadMP4Video(t *testing.T) {
	cfg := testConfig(t)
	expectedProbe := configureFakeMediaTools(t, cfg)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin
	var sawExtract bool
	var sawMerge bool
	fake := &fakeRunner{
		writeSize: 4096,
		onCall: func(_ string, args []string) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "--extract-audio") {
				sawExtract = true
			}
			if strings.Contains(joined, "--merge-output-format") {
				sawMerge = true
			}
		},
	}
	probe := &fakeProbeRunner{}
	d, err := New(cfg, Options{Runner: fake, ProbeRunner: probe})
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Download(context.Background(), Request{
		VideoID: "SX_ViT4Ra7k",
		Format:  "mp4",
		Title:   "Lemon Official",
	})
	if err != nil {
		t.Fatalf("Download mp4: %v", err)
	}
	if res.Format != "mp4" || res.ContentType != "video/mp4" {
		t.Fatalf("result meta: %+v", res)
	}
	if !strings.HasSuffix(res.Filename, ".mp4") {
		t.Fatalf("filename=%q", res.Filename)
	}
	if sawExtract {
		t.Fatal("mp4 download used extract-audio")
	}
	if !sawMerge {
		t.Fatal("mp4 download missing merge-output-format")
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	probeName, probeArgs := probe.lastCall()
	if filepath.Clean(probeName) != filepath.Clean(expectedProbe) {
		t.Fatalf("probe path=%q want %q", probeName, expectedProbe)
	}
	joinedProbe := strings.Join(probeArgs, " ")
	if !strings.Contains(joinedProbe, "-select_streams V:0") {
		t.Fatalf("probe must reject attached pictures: %v", probeArgs)
	}
}

func TestValidateMediaFileRejectsAudioOnlyMP4(t *testing.T) {
	cfg := testConfig(t)
	configureFakeMediaTools(t, cfg)
	// Minimal non-empty file without a video stream.
	p := filepath.Join(cfg.DownloadDir, "audio-only.mp4")
	if err := os.WriteFile(p, []byte("not-a-real-mp4-but-small"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProbeRunner{results: []probeResult{{stdout: ""}}}
	err := validateMediaFile(context.Background(), probe, cfg.FFmpegLocation, p, "mp4")
	if !errors.Is(err, ErrInvalidMedia) || !errors.Is(err, ErrExecFailed) {
		t.Fatalf("audio-only mp4 should be invalid/upstream failure, got %v", err)
	}
	// Audio formats should not require a video stream.
	before := probe.callCount()
	if err := validateMediaFile(context.Background(), probe, cfg.FFmpegLocation, p, "mp3"); err != nil {
		t.Fatalf("audio format should pass size-only check: %v", err)
	}
	if probe.callCount() != before {
		t.Fatal("audio validation must not invoke ffprobe")
	}

	probe = &fakeProbeRunner{results: []probeResult{{stdout: "video,1280,720\n"}}}
	if err := validateMediaFile(context.Background(), probe, cfg.FFmpegLocation, p, "mp4"); err != nil {
		t.Fatalf("video stream should pass: %v", err)
	}
}

func TestValidateMediaFileClassifiesProbeFailures(t *testing.T) {
	cfg := testConfig(t)
	configureFakeMediaTools(t, cfg)
	p := filepath.Join(cfg.DownloadDir, "candidate.mp4")
	if err := os.WriteFile(p, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}

	launchFailure := &fakeProbeRunner{results: []probeResult{{err: errors.New("launch failed")}}}
	err := validateMediaFile(context.Background(), launchFailure, cfg.FFmpegLocation, p, "mp4")
	if !errors.Is(err, ErrExecFailed) || errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("probe launch failure should not invalidate cache: %v", err)
	}
	lockedFile := &fakeProbeRunner{results: []probeResult{{stderr: "Access is denied", err: &exec.ExitError{}}}}
	err = validateMediaFile(context.Background(), lockedFile, cfg.FFmpegLocation, p, "mp4")
	if !errors.Is(err, ErrExecFailed) || errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("temporary probe access failure should preserve cache: %v", err)
	}

	badFile := &fakeProbeRunner{results: []probeResult{{stderr: "moov atom not found", err: &exec.ExitError{}}}}
	err = validateMediaFile(context.Background(), badFile, cfg.FFmpegLocation, p, "mp4")
	if !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("ffprobe rejected file should invalidate it: %v", err)
	}
}

func TestResolveFFprobePathUsesSiblingOfConfiguredFFmpeg(t *testing.T) {
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "custom-ffmpeg.exe")
	ffprobe := filepath.Join(dir, "ffprobe.exe")
	for _, path := range []string{ffmpeg, ffprobe} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveFFprobePath(ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(ffprobe) {
		t.Fatalf("resolved %q want sibling %q", got, ffprobe)
	}
}

func TestCacheInvalidateRemovesExpectedEntry(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(CacheEntry{VideoID: "abc1234", Format: "mp4", Bitrate: "0", Path: path, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("abc1234", "mp4", "0"); !ok {
		t.Fatal("expected cache hit before delete")
	}
	e, ok := c.Get("abc1234", "mp4", "0")
	if !ok {
		t.Fatal("expected cache entry")
	}
	removed, err := c.Invalidate(e)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected entry to be invalidated")
	}
	if _, ok := c.Get("abc1234", "mp4", "0"); ok {
		t.Fatal("expected miss after delete")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestCacheInvalidateDoesNotDeleteNewerReplacement(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.mp4")
	newPath := filepath.Join(dir, "new.mp4")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(CacheEntry{VideoID: "abc1234", Format: "mp4", Bitrate: "0", Path: oldPath, Size: 3}); err != nil {
		t.Fatal(err)
	}
	old, ok := c.Get("abc1234", "mp4", "0")
	if !ok {
		t.Fatal("missing old entry")
	}
	const newToken = "0123456789abcdef0123456789abcdef"
	if err := c.Put(CacheEntry{VideoID: "abc1234", Format: "mp4", Bitrate: "0", Path: newPath, Size: 3, Token: newToken}); err != nil {
		t.Fatal(err)
	}
	removed, err := c.Invalidate(old)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("stale validator removed newer cache entry")
	}
	current, ok := c.Get("abc1234", "mp4", "0")
	if !ok || current.Token != newToken || filepath.Clean(current.Path) != filepath.Clean(newPath) {
		t.Fatalf("new entry was not preserved: %+v ok=%v", current, ok)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new file removed: %v", err)
	}
}

func TestDownloadInvalidCachedMP4Redownloads(t *testing.T) {
	cfg := testConfig(t)
	configureFakeMediaTools(t, cfg)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin
	fake := &fakeRunner{writeSize: 4096}
	probe := &fakeProbeRunner{results: []probeResult{
		{stdout: ""},
		{stdout: "video,1920,1080\n"},
	}}
	d, err := New(cfg, Options{Runner: fake, ProbeRunner: probe})
	if err != nil {
		t.Fatal(err)
	}
	const videoID = "SX_ViT4Ra7k"
	oldPath := filepath.Join(cfg.DownloadDir, videoID+".mp4")
	if err := os.WriteFile(oldPath, []byte("audio-only"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.cache.Put(CacheEntry{VideoID: videoID, Format: "mp4", Bitrate: "0", Path: oldPath, Size: 10}); err != nil {
		t.Fatal(err)
	}

	res, err := d.Download(context.Background(), Request{VideoID: videoID, Format: "mp4"})
	if err != nil {
		t.Fatalf("redownload after invalid cache: %v", err)
	}
	if res.Cached {
		t.Fatal("invalid cache must be replaced by a fresh download")
	}
	if fake.callCount() != 1 || probe.callCount() != 2 {
		t.Fatalf("calls ytdlp=%d probe=%d", fake.callCount(), probe.callCount())
	}
	if st, err := os.Stat(res.Path); err != nil || st.Size() != 4096 {
		t.Fatalf("replacement file stat=%v err=%v", st, err)
	}
}

func TestLookupTokenInvalidMP4ReturnsNotFound(t *testing.T) {
	cfg := testConfig(t)
	configureFakeMediaTools(t, cfg)
	d, err := New(cfg, Options{Runner: &fakeRunner{}, ProbeRunner: &fakeProbeRunner{results: []probeResult{{stdout: ""}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.DownloadDir, "cached.mp4")
	if err := os.WriteFile(path, []byte("audio-only"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.cache.Put(CacheEntry{VideoID: "SX_ViT4Ra7k", Format: "mp4", Bitrate: "0", Path: path, Size: 10}); err != nil {
		t.Fatal(err)
	}
	e, ok := d.cache.Get("SX_ViT4Ra7k", "mp4", "0")
	if !ok {
		t.Fatal("missing cached entry")
	}
	_, err = d.LookupToken(e.Token)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid cached token should be not found, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid cached file should be removed, stat err=%v", err)
	}
}

func TestSanitizeFilenameRejectsTraversal(t *testing.T) {
	got := SanitizeFilename(`../../evil\name.mp3`)
	if strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
		t.Fatalf("unsafe filename: %q", got)
	}
	if got == "" {
		t.Fatal("empty filename")
	}
}

func TestDownloadCacheHitDoesNotRerun(t *testing.T) {
	cfg := testConfig(t)
	fake := &fakeRunner{writeSize: 2048}
	// 让 resolveYtdlpPath 找到一个假文件
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin

	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := Request{VideoID: "SJKoWAd5ySo", Title: "晴天", Artists: []string{"周杰倫"}}

	r1, err := d.Download(ctx, req)
	if err != nil {
		t.Fatalf("first download: %v", err)
	}
	if r1.Cached {
		t.Fatal("first should be miss")
	}
	if fake.callCount() != 1 {
		t.Fatalf("calls=%d want 1", fake.callCount())
	}
	if r1.Size != 2048 {
		t.Fatalf("size=%d", r1.Size)
	}
	if r1.Token == "" {
		t.Fatal("missing token")
	}

	r2, err := d.Download(ctx, req)
	if err != nil {
		t.Fatalf("second download: %v", err)
	}
	if !r2.Cached {
		t.Fatal("second should be cache hit")
	}
	if fake.callCount() != 1 {
		t.Fatalf("cache hit must not rerun ytdlp; calls=%d", fake.callCount())
	}
	if r2.Path != r1.Path || r2.Token != r1.Token {
		t.Fatalf("cache mismatch: %+v vs %+v", r1, r2)
	}

	// LookupToken
	got, err := d.LookupToken(r1.Token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Path != r1.Path {
		t.Fatalf("token path mismatch")
	}
}

func TestDownloadSingleflightTenConcurrent(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	cfg.MaxConcurrentDownloads = 4

	fake := &fakeRunner{writeSize: 4096, delay: 80 * time.Millisecond}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([]*Result, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = d.Download(context.Background(), Request{
				VideoID: "3NNhrqHZqlI",
				Title:   "Lemon",
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if fake.callCount() != 1 {
		t.Fatalf("singleflight: expected 1 ytdlp call, got %d", fake.callCount())
	}
	// 全部应拿到同一 path/token
	for i := 1; i < n; i++ {
		if results[i].Path != results[0].Path || results[i].Token != results[0].Token {
			t.Fatalf("result %d diverged", i)
		}
	}
}

func TestDownloadTooLarge(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxFilesizeMB = 1 // 1 MiB
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin

	// 写 1.5 MiB
	fake := &fakeRunner{writeSize: int(1.5 * 1024 * 1024)}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: "SJKoWAd5ySo"})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	var te *TooLargeError
	if !errors.As(err, &te) {
		t.Fatalf("want TooLargeError, got %T", err)
	}
	if te.Size <= te.MaxBytes {
		t.Fatalf("size/max inconsistent: %+v", te)
	}
}

func TestDownloadPathTraversalRejected(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	d, err := New(cfg, Options{Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: "../evil1"})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want bad request, got %v", err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: `..\evil2`})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want bad request, got %v", err)
	}
}

func TestDownloadExecFailureReadable(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	fake := &fakeRunner{fail: true, failMsg: "ERROR: Video unavailable\nfragment missing"}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: "SJKoWAd5ySo"})
	if !errors.Is(err, ErrExecFailed) {
		t.Fatalf("want ErrExecFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "Video unavailable") {
		t.Fatalf("error should include stderr: %v", err)
	}
}

func TestDownloadRetriesTransientThenSucceeds(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin
	cfg.DownloadTimeout = 20 * time.Second

	fake := &fakeRunner{
		writeSize: 2048,
		failTimes: 2,
		failMsg:   "HTTP Error 403: Forbidden",
	}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Download(context.Background(), Request{VideoID: "3NNhrqHZqlI", Title: "Lemon"})
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if res == nil || res.Size <= 0 {
		t.Fatalf("bad result: %+v", res)
	}
	if fake.callCount() < 3 {
		t.Fatalf("expected at least 3 strategy attempts, got %d", fake.callCount())
	}
}

func TestDownloadHardPermanentDoesNotSpinAllStrategies(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin

	fake := &fakeRunner{
		fail:    true,
		failMsg: "Private video. Sign in if you've been granted access",
	}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: "3NNhrqHZqlI"})
	if !errors.Is(err, ErrExecFailed) {
		t.Fatalf("want ErrExecFailed, got %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("private video should fail fast, attempts=%d", fake.callCount())
	}
}

func TestIsTransientYtdlpError(t *testing.T) {
	if !isTransientYtdlpError(&ExecError{Stderr: "HTTP Error 403: Forbidden"}) {
		t.Fatal("403 should be transient")
	}
	if isTransientYtdlpError(&ExecError{Stderr: "Private video"}) {
		t.Fatal("private should be permanent")
	}
}

func TestYtdlpMissingClearError(t *testing.T) {
	cfg := testConfig(t)
	cfg.YtdlpPath = filepath.Join(cfg.DownloadDir, "definitely-missing-ytdlp.exe")
	// 用真实 ExecRunner，resolve 阶段就会失败
	d, err := New(cfg, Options{Runner: ExecRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Download(context.Background(), Request{VideoID: "SJKoWAd5ySo"})
	if !errors.Is(err, ErrYtdlpMissing) {
		t.Fatalf("want ErrYtdlpMissing, got %v", err)
	}
	if !strings.Contains(err.Error(), "YTDLP_PATH") {
		t.Fatalf("should mention YTDLP_PATH: %v", err)
	}
}

func TestInvalidToken(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	d, err := New(cfg, Options{Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.LookupToken("../etc/passwd")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want bad request, got %v", err)
	}
	_, err = d.LookupToken("deadbeef") // too short
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want bad request short token, got %v", err)
	}
	_, err = d.LookupToken("0123456789abcdef0123456789abcdef")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestCacheIndexPersists(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	fake := &fakeRunner{writeSize: 512}
	d1, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	r, err := d1.Download(context.Background(), Request{VideoID: "SJKoWAd5ySo", Title: "Rain"})
	if err != nil {
		t.Fatal(err)
	}
	// 新 downloader 读同一目录索引
	d2, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := d2.Download(context.Background(), Request{VideoID: "SJKoWAd5ySo"})
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Cached {
		t.Fatal("reloaded cache should hit")
	}
	if r2.Token != r.Token {
		t.Fatalf("token changed across reload: %s vs %s", r.Token, r2.Token)
	}
	if fake.callCount() != 1 {
		t.Fatalf("calls=%d want 1", fake.callCount())
	}
}

func TestSemaphoreLimitsConcurrency(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	cfg.MaxConcurrentDownloads = 1

	var current, maxSeen int32
	fake := &fakeRunner{
		writeSize: 256,
		delay:     50 * time.Millisecond,
		onCall: func(name string, args []string) {
			v := atomic.AddInt32(&current, 1)
			for {
				old := atomic.LoadInt32(&maxSeen)
				if v <= old || atomic.CompareAndSwapInt32(&maxSeen, old, v) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt32(&current, -1)
		},
	}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := []string{"aaaaaa1", "bbbbbb2", "cccccc3"}
	wg.Add(len(ids))
	for _, id := range ids {
		id := id
		go func() {
			defer wg.Done()
			_, err := d.Download(context.Background(), Request{VideoID: id})
			if err != nil {
				t.Errorf("download %s: %v", id, err)
			}
		}()
	}
	wg.Wait()
	if maxSeen > 1 {
		t.Fatalf("semaphore max concurrent=%d want <=1", maxSeen)
	}
	if fake.callCount() != 3 {
		t.Fatalf("calls=%d want 3", fake.callCount())
	}
}

func TestLiveDownloadLemon(t *testing.T) {
	if os.Getenv("YTM_SKIP_LIVE") == "1" {
		t.Skip("YTM_SKIP_LIVE=1")
	}
	ytdlp := os.Getenv("YTDLP_PATH")
	if ytdlp == "" {
		// Prefer project-local standalone binary only (no external tool dirs).
		candidates := []string{
			filepath.Join("bin", "yt-dlp.exe"),
			filepath.Join("bin", "yt-dlp"),
		}
		for _, c := range candidates {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				ytdlp = c
				break
			}
		}
	}
	if ytdlp == "" {
		if p, err := exec.LookPath("yt-dlp"); err == nil {
			ytdlp = p
		} else if p, err := exec.LookPath("yt-dlp.exe"); err == nil {
			ytdlp = p
		}
	}
	if ytdlp == "" {
		t.Skip("yt-dlp not found; set YTDLP_PATH or place bin/yt-dlp.exe")
	}
	ffmpeg := os.Getenv("FFMPEG_LOCATION")
	if ffmpeg == "" {
		// Prefer project-local copy under bin/.
		for _, c := range []string{filepath.Join("bin", "ffmpeg.exe"), filepath.Join("bin", "ffmpeg"), "bin"} {
			if _, err := os.Stat(c); err == nil {
				ffmpeg = c
				break
			}
		}
	}

	dir := t.TempDir()
	cfg := &config.Config{
		DownloadDir:            dir,
		AudioFormat:            "mp3",
		AudioBitrate:           "192",
		YtdlpPath:              ytdlp,
		FFmpegLocation:         ffmpeg,
		MaxConcurrentDownloads: 1,
		MaxFilesizeMB:          50,
		DownloadTimeout:        5 * time.Minute,
		CacheTTL:               time.Hour,
		CacheMaxTotalMB:        100,
	}
	d, err := New(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Lemon - Kenshi Yonezu
	const videoID = "3NNhrqHZqlI"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := d.Download(ctx, Request{
		VideoID:         videoID,
		Title:           "Lemon",
		Artists:         []string{"Kenshi Yonezu"},
		DisplayName:     "Lemon - Kenshi Yonezu",
		DurationSeconds: 257,
	})
	if err != nil {
		t.Fatalf("live download: %v", err)
	}
	if res.Cached {
		t.Fatal("first live download should miss cache")
	}
	if res.Size <= 0 {
		t.Fatal("empty file")
	}
	st, err := os.Stat(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != res.Size {
		t.Fatalf("size mismatch stat=%d res=%d", st.Size(), res.Size)
	}

	// ffprobe validation prefers project bin/
	ffprobe := ""
	for _, c := range []string{
		filepath.Join("bin", "ffprobe.exe"),
		filepath.Join("bin", "ffprobe"),
		filepath.Join(ffmpeg, "ffprobe.exe"),
		filepath.Join(ffmpeg, "ffprobe"),
	} {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			ffprobe = c
			break
		}
	}
	if ffprobe == "" {
		if p, err := exec.LookPath("ffprobe"); err == nil {
			ffprobe = p
		}
	}
	if ffprobe == "" {
		t.Logf("ffprobe not found, skip media probe; file size=%d", res.Size)
		return
	}
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration,bit_rate", "-of", "json", res.Path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "duration") {
		t.Fatalf("ffprobe missing duration: %s", s)
	}
	t.Logf("ffprobe: %s", s)

	// 缓存命中
	res2, err := d.Download(ctx, Request{VideoID: videoID})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Cached {
		t.Fatal("second live should hit cache")
	}
}

func TestDownloadSingleflightIgnoresCallerCancel(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	_ = os.WriteFile(fakeBin, []byte("fake"), 0o755)
	cfg.YtdlpPath = fakeBin
	cfg.MaxConcurrentDownloads = 2

	// Slow enough that we can cancel the first waiter mid-flight.
	fake := &fakeRunner{writeSize: 2048, delay: 200 * time.Millisecond}
	d, err := New(cfg, Options{Runner: fake})
	if err != nil {
		t.Fatal(err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	type outcome struct {
		res *Result
		err error
	}
	ch1 := make(chan outcome, 1)
	ch2 := make(chan outcome, 1)

	go func() {
		res, err := d.Download(ctx1, Request{VideoID: "3NNhrqHZqlI", Title: "Lemon"})
		ch1 <- outcome{res, err}
	}()

	// Let the first request enter singleflight / yt-dlp.
	time.Sleep(30 * time.Millisecond)
	cancel1()

	go func() {
		res, err := d.Download(context.Background(), Request{VideoID: "3NNhrqHZqlI", Title: "Lemon"})
		ch2 <- outcome{res, err}
	}()

	o1 := <-ch1
	o2 := <-ch2

	// Second waiter must succeed even if the first caller canceled.
	if o2.err != nil {
		t.Fatalf("second download failed after first cancel: %v", o2.err)
	}
	if o2.res == nil || o2.res.Path == "" {
		t.Fatalf("second result empty: %+v", o2.res)
	}
	// yt-dlp should still only run once for the shared key.
	if fake.callCount() != 1 {
		t.Fatalf("expected 1 ytdlp call, got %d", fake.callCount())
	}
	// First may fail with canceled or still succeed depending on timing;
	// either is acceptable as long as it does not poison the shared flight.
	if o1.err != nil && !errors.Is(o1.err, context.Canceled) {
		// WithoutCancel means first may still succeed; if error, it should be cancel-related only.
		// Accept success or cancel only.
		if !strings.Contains(o1.err.Error(), "cancel") {
			t.Fatalf("unexpected first error: %v", o1.err)
		}
	}
}
