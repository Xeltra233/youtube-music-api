// Command e2e runs live end-to-end paths and light performance benchmarks
// against a running (or self-started) ytmusic-bridge instance.
//
// Usage:
//
//	go run ./cmd/e2e
//	go run ./cmd/e2e -base http://127.0.0.1:8787 -skip-start
//	go run ./cmd/e2e -query "lemon kenshi yonezu"
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type searchResponse struct {
	SessionID string       `json:"session_id"`
	Query     string       `json:"query"`
	Total     int          `json:"total"`
	Results   []resultItem `json:"results"`
}

type resultItem struct {
	Index           int      `json:"index"`
	DisplayName     string   `json:"display_name"`
	Title           string   `json:"title"`
	Artists         []string `json:"artists"`
	DurationSeconds int      `json:"duration_seconds"`
	VideoID         string   `json:"video_id"`
	MatchScore      float64  `json:"match_score"`
}

type downloadJSON struct {
	Title           string `json:"title"`
	DisplayName     string `json:"display_name"`
	VideoID         string `json:"video_id"`
	DurationSeconds int    `json:"duration_seconds"`
	Format          string `json:"format"`
	Filesize        int64  `json:"filesize"`
	FileURL         string `json:"file_url"`
	Cached          bool   `json:"cached"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail"`
}

type probeInfo struct {
	Duration float64 `json:"duration"`
	BitRate  int64   `json:"bit_rate"`
	Format   string  `json:"format"`
}

type latencyStats struct {
	Count int           `json:"count"`
	Min   time.Duration `json:"min"`
	Max   time.Duration `json:"max"`
	P50   time.Duration `json:"p50"`
	P90   time.Duration `json:"p90"`
	P99   time.Duration `json:"p99"`
	Avg   time.Duration `json:"avg"`
}

type report struct {
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	BaseURL         string         `json:"base_url"`
	Query           string         `json:"query"`
	Healthz         map[string]any `json:"healthz"`
	IndexPath       pathResult     `json:"index_path"`
	NamePath        pathResult     `json:"name_path"`
	CacheHit        pathResult     `json:"cache_hit"`
	SearchBench     benchSearch    `json:"search_bench"`
	DownloadBench   benchDownload  `json:"download_bench"`
	ProcessMemoryMB float64        `json:"process_memory_mb"`
	OK              bool           `json:"ok"`
}

type pathResult struct {
	OK          bool      `json:"ok"`
	SessionID   string    `json:"session_id,omitempty"`
	Index       int       `json:"index,omitempty"`
	Name        string    `json:"name,omitempty"`
	VideoID     string    `json:"video_id,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	Cached      bool      `json:"cached"`
	XCache      string    `json:"x_cache,omitempty"`
	Filesize    int64     `json:"filesize"`
	LatencyMS   float64   `json:"latency_ms"`
	Probe       probeInfo `json:"probe"`
	Error       string    `json:"error,omitempty"`
}

type benchSearch struct {
	Concurrency int          `json:"concurrency"`
	Requests    int          `json:"requests"`
	Success     int          `json:"success"`
	Failed      int          `json:"failed"`
	DurationMS  float64      `json:"duration_ms"`
	QPS         float64      `json:"qps"`
	Latency     latencyStats `json:"latency"`
	Error       string       `json:"error,omitempty"`
}

type benchDownload struct {
	Concurrency    int          `json:"concurrency"`
	Success        int          `json:"success"`
	Failed         int          `json:"failed"`
	CacheHits      int          `json:"cache_hits"`
	CacheMisses    int          `json:"cache_misses"`
	DurationMS     float64      `json:"duration_ms"`
	FirstLatencyMS float64      `json:"first_latency_ms"`
	Latency        latencyStats `json:"latency"`
	// Evidence: API does not expose yt-dlp process count. We treat
	// "<=1 cold miss + rest hits under 20 concurrent same video" as
	// singleflight+cache evidence.
	Evidence string `json:"evidence,omitempty"`
	Error    string `json:"error,omitempty"`
}

func main() {
	var (
		baseURL     = flag.String("base", envOr("E2E_BASE", "http://127.0.0.1:8787"), "service base URL")
		query       = flag.String("query", envOr("E2E_QUERY", "lemon kenshi yonezu"), "search query")
		skipStart   = flag.Bool("skip-start", false, "do not start local server; require existing instance")
		keepServer  = flag.Bool("keep-server", false, "leave started server running")
		searchConc  = flag.Int("search-concurrency", 20, "search bench concurrency")
		searchN     = flag.Int("search-requests", 60, "search bench total requests")
		dlConc      = flag.Int("download-concurrency", 20, "same-song concurrent download workers")
		outJSON     = flag.String("out", "goal-1/e2e-report.json", "write JSON report path (empty to skip)")
		ffprobePath = flag.String("ffprobe", envOr("FFPROBE_PATH", defaultFFProbe()), "ffprobe executable")
		timeout     = flag.Duration("timeout", 8*time.Minute, "overall deadline")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	rep := &report{
		StartedAt: time.Now(),
		BaseURL:   strings.TrimRight(*baseURL, "/"),
		Query:     *query,
	}

	root, err := os.Getwd()
	if err != nil {
		fatalf("getwd: %v", err)
	}

	var serverCmd *exec.Cmd
	if !*skipStart {
		if err := waitHealth(ctx, rep.BaseURL, 800*time.Millisecond); err != nil {
			serverCmd, err = startServer(root, rep.BaseURL)
			if err != nil {
				fatalf("start server: %v", err)
			}
			if !*keepServer {
				defer stopServer(serverCmd)
			}
			if err := waitHealth(ctx, rep.BaseURL, 30*time.Second); err != nil {
				fatalf("healthz after start: %v", err)
			}
		} else {
			fmt.Println("reuse already-running server at", rep.BaseURL)
		}
	} else if err := waitHealth(ctx, rep.BaseURL, 5*time.Second); err != nil {
		fatalf("healthz: %v", err)
	}

	client := &http.Client{Timeout: 6 * time.Minute}
	health, err := getHealthz(ctx, client, rep.BaseURL)
	if err != nil {
		fatalf("healthz body: %v", err)
	}
	rep.Healthz = health
	fmt.Printf("healthz ok: version=%v ytdlp=%v\n", health["version"], health["ytdlp"])

	rep.IndexPath = runIndexPath(ctx, client, rep.BaseURL, *query, *ffprobePath)
	printPath("index", rep.IndexPath)

	rep.NamePath = runNamePath(ctx, client, rep.BaseURL, *query, *ffprobePath)
	printPath("name", rep.NamePath)

	videoID := firstNonEmpty(rep.IndexPath.VideoID, rep.NamePath.VideoID)
	if videoID == "" {
		rep.CacheHit.Error = "no video_id from previous paths"
	} else {
		rep.CacheHit = runCacheHit(ctx, client, rep.BaseURL, videoID, *ffprobePath)
	}
	printPath("cache", rep.CacheHit)

	rep.SearchBench = runSearchBench(ctx, client, rep.BaseURL, *query, *searchConc, *searchN)
	printSearchBench(rep.SearchBench)

	usedVideos := []string{rep.IndexPath.VideoID, rep.NamePath.VideoID}
	benchVideo := pickFreshVideo(ctx, client, rep.BaseURL, *query, usedVideos)
	if benchVideo == "" {
		// Fall back to any known id; may already be cached (still validates concurrency).
		benchVideo = firstNonEmpty(append(usedVideos, pickAltVideo(ctx, client, rep.BaseURL, *query, ""))...)
	}
	if benchVideo == "" {
		rep.DownloadBench.Error = "no video_id for download bench"
	} else {
		rep.DownloadBench = runDownloadBench(ctx, client, rep.BaseURL, benchVideo, *dlConc)
	}
	printDownloadBench(rep.DownloadBench)

	rep.ProcessMemoryMB = processMemoryMB()
	fmt.Printf("process memory (self e2e tool): %.1f MB\n", rep.ProcessMemoryMB)
	if mb, err := serverMemoryMB(serverCmd); err == nil {
		fmt.Printf("server WorkingSet: %.1f MB\n", mb)
		rep.ProcessMemoryMB = mb
	}

	rep.FinishedAt = time.Now()
	rep.OK = rep.IndexPath.OK && rep.NamePath.OK && rep.CacheHit.OK &&
		rep.SearchBench.Failed == 0 && rep.SearchBench.Success > 0 &&
		rep.DownloadBench.Failed == 0 && rep.DownloadBench.Success == *dlConc &&
		rep.DownloadBench.Error == ""

	if *outJSON != "" {
		if err := writeReport(*outJSON, rep); err != nil {
			fatalf("write report: %v", err)
		}
		fmt.Println("report:", *outJSON)
	}

	if !rep.OK {
		fmt.Fprintln(os.Stderr, "E2E FAILED")
		os.Exit(1)
	}
	fmt.Println("E2E OK")
}

func runIndexPath(ctx context.Context, client *http.Client, base, query, ffprobe string) pathResult {
	out := pathResult{}
	sr, err := doSearch(ctx, client, base, query, 10)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(sr.Results) == 0 {
		out.Error = "search returned 0 results"
		return out
	}

	var body []byte
	var hdr http.Header
	var lastErr error
	for try, item := range sr.Results {
		if try > 2 { // try at most first 3 candidates on transient upstream errors
			break
		}
		out.SessionID = sr.SessionID
		out.Index = item.Index
		out.VideoID = item.VideoID
		out.DisplayName = item.DisplayName
		start := time.Now()
		body, hdr, err = doDownloadBinary(ctx, client, base, map[string]any{
			"session_id": sr.SessionID,
			"index":      item.Index,
		})
		out.LatencyMS = float64(time.Since(start).Milliseconds())
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		// Prefer next candidate on upstream/transient failures.
		if !strings.Contains(err.Error(), "UPSTREAM_ERROR") && !strings.Contains(err.Error(), "yt-dlp failed") {
			break
		}
	}
	if lastErr != nil {
		out.Error = lastErr.Error()
		return out
	}
	out.Filesize = int64(len(body))
	out.XCache = hdr.Get("X-Cache")
	out.Cached = strings.EqualFold(out.XCache, "hit")
	if vid := hdr.Get("X-Track-Video-Id"); vid != "" {
		out.VideoID = vid
	}
	probe, err := ffprobeBytes(ctx, ffprobe, body, "mp3")
	if err != nil {
		out.Error = "ffprobe: " + err.Error()
		return out
	}
	out.Probe = probe
	if probe.Duration < 30 {
		out.Error = fmt.Sprintf("duration too short: %.2fs", probe.Duration)
		return out
	}
	out.OK = true
	return out
}

func runNamePath(ctx context.Context, client *http.Client, base, query, ffprobe string) pathResult {
	out := pathResult{}
	sr, err := doSearch(ctx, client, base, query, 10)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(sr.Results) == 0 {
		out.Error = "search returned 0 results"
		return out
	}
	item := sr.Results[0]
	if len(sr.Results) > 1 {
		item = sr.Results[1]
	}
	out.SessionID = sr.SessionID
	out.Index = item.Index
	out.Name = item.DisplayName
	out.VideoID = item.VideoID
	out.DisplayName = item.DisplayName

	start := time.Now()
	dj, err := doDownloadJSON(ctx, client, base, map[string]any{
		"session_id": sr.SessionID,
		"name":       item.DisplayName,
	})
	out.LatencyMS = float64(time.Since(start).Milliseconds())
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Cached = dj.Cached
	out.Filesize = dj.Filesize
	out.VideoID = dj.VideoID
	out.DisplayName = dj.DisplayName
	if dj.Cached {
		out.XCache = "hit"
	} else {
		out.XCache = "miss"
	}

	audio, _, err := doGETBytes(ctx, client, base+dj.FileURL)
	if err != nil {
		out.Error = "file_url: " + err.Error()
		return out
	}
	if int64(len(audio)) != dj.Filesize && dj.Filesize > 0 {
		out.Filesize = int64(len(audio))
	}
	probe, err := ffprobeBytes(ctx, ffprobe, audio, dj.Format)
	if err != nil {
		out.Error = "ffprobe: " + err.Error()
		return out
	}
	out.Probe = probe
	if probe.Duration < 30 {
		out.Error = fmt.Sprintf("duration too short: %.2fs", probe.Duration)
		return out
	}
	out.OK = true
	return out
}

func runCacheHit(ctx context.Context, client *http.Client, base, videoID, ffprobe string) pathResult {
	out := pathResult{VideoID: videoID}
	start := time.Now()
	dj, err := doDownloadJSON(ctx, client, base, map[string]any{
		"video_id": videoID,
	})
	out.LatencyMS = float64(time.Since(start).Milliseconds())
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Cached = dj.Cached
	out.Filesize = dj.Filesize
	out.DisplayName = dj.DisplayName
	if !dj.Cached {
		out.Error = "expected cached=true on second download"
		return out
	}
	out.XCache = "hit"
	audio, hdr, err := doGETBytes(ctx, client, base+dj.FileURL)
	if err != nil {
		out.Error = "file_url: " + err.Error()
		return out
	}
	if xc := hdr.Get("X-Cache"); xc != "" {
		out.XCache = xc
	}
	probe, err := ffprobeBytes(ctx, ffprobe, audio, dj.Format)
	if err != nil {
		out.Error = "ffprobe: " + err.Error()
		return out
	}
	out.Probe = probe
	out.OK = true
	return out
}

func runSearchBench(ctx context.Context, client *http.Client, base, query string, conc, total int) benchSearch {
	out := benchSearch{Concurrency: conc, Requests: total}
	if conc < 1 {
		conc = 1
	}
	if total < 1 {
		total = conc
	}
	lat := make([]time.Duration, 0, total)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var success, failed atomic.Int64
	sem := make(chan struct{}, conc)
	start := time.Now()
	for i := 0; i < total; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			q := query
			if i%5 == 0 {
				q = query + " "
			}
			t0 := time.Now()
			_, err := doSearch(ctx, client, base, q, 10)
			d := time.Since(t0)
			mu.Lock()
			lat = append(lat, d)
			mu.Unlock()
			if err != nil {
				failed.Add(1)
				return
			}
			success.Add(1)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	out.Success = int(success.Load())
	out.Failed = int(failed.Load())
	out.DurationMS = float64(elapsed.Milliseconds())
	if elapsed > 0 {
		out.QPS = float64(out.Success) / elapsed.Seconds()
	}
	out.Latency = summarizeLatency(lat)
	if out.Failed > 0 {
		out.Error = fmt.Sprintf("%d search requests failed", out.Failed)
	}
	return out
}

func runDownloadBench(ctx context.Context, client *http.Client, base, videoID string, conc int) benchDownload {
	out := benchDownload{Concurrency: conc}
	if conc < 1 {
		conc = 1
	}
	lat := make([]time.Duration, 0, conc)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var success, failed, hits, misses atomic.Int64
	var firstMS atomic.Int64
	firstMS.Store(-1)

	start := time.Now()
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			dj, err := doDownloadJSON(ctx, client, base, map[string]any{
				"video_id": videoID,
			})
			d := time.Since(t0)
			mu.Lock()
			lat = append(lat, d)
			mu.Unlock()
			for {
				cur := firstMS.Load()
				ms := d.Milliseconds()
				if cur >= 0 && ms >= cur {
					break
				}
				if firstMS.CompareAndSwap(cur, ms) {
					break
				}
			}
			if err != nil {
				failed.Add(1)
				return
			}
			success.Add(1)
			if dj.Cached {
				hits.Add(1)
			} else {
				misses.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	out.Success = int(success.Load())
	out.Failed = int(failed.Load())
	out.CacheHits = int(hits.Load())
	out.CacheMisses = int(misses.Load())
	out.DurationMS = float64(elapsed.Milliseconds())
	if v := firstMS.Load(); v >= 0 {
		out.FirstLatencyMS = float64(v)
	}
	out.Latency = summarizeLatency(lat)

	// singleflight shares one cold Result (Cached=false) with all waiters.
	// Strong evidence is wall-clock ~ one download, not miss_count==1.
	// Follow with one more request which must be a pure cache hit.
	post, postErr := doDownloadJSON(ctx, client, base, map[string]any{"video_id": videoID})
	postCached := false
	if postErr == nil {
		postCached = post.Cached
	}

	out.Evidence = fmt.Sprintf(
		"same video_id=%s, workers=%d, wall=%.0fms, p99=%.0fms, reported_misses=%d reported_hits=%d, post_cached=%v",
		videoID, conc, out.DurationMS, float64(out.Latency.P99.Milliseconds()), out.CacheMisses, out.CacheHits, postCached,
	)
	if out.Failed > 0 {
		out.Error = fmt.Sprintf("%d download requests failed", out.Failed)
	} else if out.DurationMS > 20000 {
		// One song download is typically 4-8s; 20 sequential would be minutes.
		out.Error = fmt.Sprintf("wall clock too high for singleflight: %.0fms", out.DurationMS)
	} else if postErr != nil {
		out.Error = "post concurrent download failed: " + postErr.Error()
	} else if !postCached {
		out.Error = "expected cached=true after concurrent wave"
	}
	return out
}

func doSearch(ctx context.Context, client *http.Client, base, query string, limit int) (*searchResponse, error) {
	var out searchResponse
	if err := doJSON(ctx, client, http.MethodPost, base+"/search", map[string]any{
		"query": query,
		"limit": limit,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func doDownloadJSON(ctx context.Context, client *http.Client, base string, body map[string]any) (*downloadJSON, error) {
	var out downloadJSON
	if err := doJSON(ctx, client, http.MethodPost, base+"/download?mode=json", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func doDownloadBinary(ctx context.Context, client *http.Client, base string, body map[string]any) ([]byte, http.Header, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/download", bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 80<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeErrBody(data))
	}
	return data, resp.Header, nil
}

func doGETBytes(ctx context.Context, client *http.Client, url string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 80<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeErrBody(data))
	}
	return data, resp.Header, nil
}

func doJSON(ctx context.Context, client *http.Client, method, url string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeErrBody(data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode json: %w; body=%s", err, truncate(string(data), 200))
	}
	return nil
}

func getHealthz(ctx context.Context, client *http.Client, base string) (map[string]any, error) {
	var out map[string]any
	if err := doJSON(ctx, client, http.MethodGet, base+"/healthz", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func waitHealth(ctx context.Context, base string, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := getHealthz(ctx, client, base)
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			if last == nil {
				last = errors.New("timeout waiting healthz")
			}
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startServer(root, base string) (*exec.Cmd, error) {
	host, port, err := splitBase(base)
	if err != nil {
		return nil, err
	}
	exe := filepath.Join(root, "bin", "ytmusic-bridge.exe")
	if _, err := os.Stat(exe); err != nil {
		build := exec.Command("go", "build", "-o", exe, "./cmd/ytmusic-bridge")
		build.Dir = root
		build.Env = withGoEnv(os.Environ(), root)
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return nil, fmt.Errorf("go build: %w", err)
		}
	}

	cmd := exec.Command(exe)
	cmd.Dir = root
	cmd.Env = append(withGoEnv(os.Environ(), root),
		"HOST="+host,
		"PORT="+port,
		"YTDLP_PATH="+filepath.Join(root, "bin", "yt-dlp.exe"),
		"FFMPEG_LOCATION="+localFFmpegLocation(root),
	)
	logPath := filepath.Join(root, "goal-1", "e2e-server.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	fmt.Printf("started server pid=%d log=%s\n", cmd.Process.Pid, logPath)
	return cmd, nil
}

func stopServer(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func splitBase(base string) (host, port string, err error) {
	u := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	host, port, err = net.SplitHostPort(u)
	if err != nil {
		return "", "", fmt.Errorf("invalid base %q: %w", base, err)
	}
	return host, port, nil
}

func ffprobeBytes(ctx context.Context, ffprobe string, data []byte, format string) (probeInfo, error) {
	if strings.TrimSpace(ffprobe) == "" {
		return probeInfo{}, errors.New("ffprobe path empty")
	}
	tmpDir, err := os.MkdirTemp("", "ytm-e2e-*")
	if err != nil {
		return probeInfo{}, err
	}
	defer os.RemoveAll(tmpDir)
	ext := format
	if ext == "" {
		ext = "mp3"
	}
	path := filepath.Join(tmpDir, "audio."+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return probeInfo{}, err
	}
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-show_entries", "format=duration,bit_rate,format_name",
		"-of", "json",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return probeInfo{}, fmt.Errorf("%w: %s", err, truncate(string(ee.Stderr), 300))
		}
		return probeInfo{}, err
	}
	var parsed struct {
		Format struct {
			Duration   string `json:"duration"`
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return probeInfo{}, err
	}
	dur, _ := strconv.ParseFloat(parsed.Format.Duration, 64)
	br, _ := strconv.ParseInt(parsed.Format.BitRate, 10, 64)
	return probeInfo{
		Duration: dur,
		BitRate:  br,
		Format:   parsed.Format.FormatName,
	}, nil
}

func summarizeLatency(samples []time.Duration) latencyStats {
	if len(samples) == 0 {
		return latencyStats{}
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}
	return latencyStats{
		Count: len(sorted),
		Min:   sorted[0],
		Max:   sorted[len(sorted)-1],
		P50:   percentile(sorted, 50),
		P90:   percentile(sorted, 90),
		P99:   percentile(sorted, 99),
		Avg:   sum / time.Duration(len(sorted)),
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int((float64(p) / 100.0) * float64(len(sorted)))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func pickAltVideo(ctx context.Context, client *http.Client, base, query, avoid string) string {
	return pickFreshVideo(ctx, client, base, query, []string{avoid})
}

func pickFreshVideo(ctx context.Context, client *http.Client, base, query string, avoid []string) string {
	sr, err := doSearch(ctx, client, base, query, 10)
	if err != nil || len(sr.Results) == 0 {
		return ""
	}
	blocked := map[string]struct{}{}
	for _, v := range avoid {
		v = strings.TrimSpace(v)
		if v != "" {
			blocked[v] = struct{}{}
		}
	}
	for _, it := range sr.Results {
		if it.VideoID == "" {
			continue
		}
		if _, ok := blocked[it.VideoID]; ok {
			continue
		}
		return it.VideoID
	}
	return ""
}

func writeReport(path string, rep *report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func processMemoryMB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.Alloc) / (1024 * 1024)
}

func serverMemoryMB(cmd *exec.Cmd) (float64, error) {
	if cmd == nil || cmd.Process == nil {
		return 0, errors.New("no server process")
	}
	ps := fmt.Sprintf(
		"(Get-Process -Id %d -ErrorAction Stop).WorkingSet64",
		cmd.Process.Pid,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, err
	}
	return float64(n) / (1024 * 1024), nil
}

func withGoEnv(env []string, root string) []string {
	out := make([]string, 0, len(env)+2)
	hasCache := false
	for _, e := range env {
		if strings.HasPrefix(e, "GOCACHE=") {
			hasCache = true
		}
		out = append(out, e)
	}
	if !hasCache {
		out = append(out, "GOCACHE="+filepath.Join(root, ".gocache"))
	}
	return out
}

func defaultFFProbe() string {
	candidates := []string{
		filepath.Join("bin", "ffprobe.exe"),
		filepath.Join("bin", "ffprobe"),
		"ffprobe",
	}
	for _, c := range candidates {
		if c == "ffprobe" {
			if p, err := exec.LookPath(c); err == nil {
				return p
			}
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return "ffprobe"
}

func localFFmpegLocation(root string) string {
	for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
		p := filepath.Join(root, "bin", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Directory form is also accepted by yt-dlp.
	dir := filepath.Join(root, "bin")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func summarizeErrBody(data []byte) string {
	var ae apiError
	if err := json.Unmarshal(data, &ae); err == nil && ae.Code != "" {
		return fmt.Sprintf("%s: %s", ae.Code, ae.Message)
	}
	return truncate(string(data), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func printPath(name string, p pathResult) {
	if p.OK {
		fmt.Printf("[%s] OK video=%s cached=%v x-cache=%s size=%d duration=%.1fs bitrate=%d latency=%.0fms\n",
			name, p.VideoID, p.Cached, p.XCache, p.Filesize, p.Probe.Duration, p.Probe.BitRate, p.LatencyMS)
		return
	}
	fmt.Printf("[%s] FAIL: %s\n", name, p.Error)
}

func printSearchBench(b benchSearch) {
	fmt.Printf("[search-bench] success=%d/%d qps=%.2f p50=%s p99=%s wall=%.0fms err=%s\n",
		b.Success, b.Requests, b.QPS, b.Latency.P50, b.Latency.P99, b.DurationMS, b.Error)
}

func printDownloadBench(b benchDownload) {
	fmt.Printf("[download-bench] success=%d/%d misses=%d hits=%d wall=%.0fms p99=%s evidence=%s err=%s\n",
		b.Success, b.Concurrency, b.CacheMisses, b.CacheHits, b.DurationMS, b.Latency.P99, b.Evidence, b.Error)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
