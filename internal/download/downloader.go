package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/cookies"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

// Request 是一次下载请求。
type Request struct {
	VideoID         string
	Format          string // 空则用配置默认
	Title           string
	Artists         []string
	DisplayName     string
	DurationSeconds int
}

// Result 是下载结果（命中缓存或新下载）。
type Result struct {
	Path            string
	Size            int64
	Cached          bool
	Token           string
	Format          string
	Bitrate         string
	VideoID         string
	Title           string
	Artists         []string
	DisplayName     string
	DurationSeconds int
	ExpiresIn       int // 秒
	ContentType     string
	Filename        string
}

// Downloader 负责缓存 + singleflight + 信号量 + yt-dlp。
type Downloader struct {
	cfg    *config.Config
	cache  *Cache
	runner CommandRunner
	sem    *semaphore.Weighted
	group  singleflight.Group
	now    func() time.Time
	probe  CommandRunner
}

// Options 可覆盖 runner（测试注入 fake）。
type Options struct {
	Runner      CommandRunner
	ProbeRunner CommandRunner
	Now         func() time.Time
}

// New 创建下载器。
func New(cfg *config.Config, opts Options) (*Downloader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("download: nil config")
	}
	if err := cfg.EnsureDownloadDir(); err != nil {
		return nil, err
	}
	cache, err := newCache(cfg.DownloadDir, cfg.CacheTTL)
	if err != nil {
		return nil, err
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	probe := opts.ProbeRunner
	if probe == nil {
		probe = ExecRunner{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	n := int64(cfg.MaxConcurrentDownloads)
	if n < 1 {
		n = 1
	}
	d := &Downloader{
		cfg:    cfg,
		cache:  cache,
		runner: runner,
		probe:  probe,
		sem:    semaphore.NewWeighted(n),
		now:    now,
	}
	d.cache.now = now
	return d, nil
}

// Download 按 videoID+format 下载或返回缓存。
func (d *Downloader) Download(ctx context.Context, req Request) (*Result, error) {
	if d == nil {
		return nil, fmt.Errorf("download: nil downloader")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	videoID := strings.TrimSpace(req.VideoID)
	if err := ValidateVideoID(videoID); err != nil {
		return nil, err
	}
	format, err := NormalizeFormat(req.Format, d.cfg.AudioFormat)
	if err != nil {
		return nil, err
	}
	bitrate := "0"
	if !IsVideoFormat(format) {
		bitrate = d.cfg.AudioBitrate
		if bitrate == "" {
			bitrate = "192"
		}
	}

	// 缓存命中：不占信号量、不跑 yt-dlp。
	if e, ok := d.cache.Get(videoID, format, bitrate); ok {
		if err := d.validateCachedEntry(ctx, e, format); err == nil {
			return d.resultFromEntry(e, true), nil
		} else if !errors.Is(err, ErrInvalidMedia) {
			return nil, err
		}
	}

	key := cacheKey(videoID, format, bitrate)
	// Detach from caller cancel so one canceled waiter does not poison
	// other concurrent singleflight sharers of the same key.
	// DownloadTimeout still bounds the actual yt-dlp work.
	workCtx := context.WithoutCancel(ctx)
	v, err, _ := d.group.Do(key, func() (any, error) {
		// double-check cache inside singleflight
		if e, ok := d.cache.Get(videoID, format, bitrate); ok {
			if err := d.validateCachedEntry(workCtx, e, format); err == nil {
				return d.resultFromEntry(e, true), nil
			} else if !errors.Is(err, ErrInvalidMedia) {
				return nil, err
			}
		}
		return d.downloadOnce(workCtx, req, videoID, format, bitrate)
	})
	if err != nil {
		return nil, err
	}
	res, ok := v.(*Result)
	if !ok || res == nil {
		return nil, fmt.Errorf("download: unexpected singleflight result type %T", v)
	}
	// 结果拷贝，避免并发改写
	out := *res
	return &out, nil
}

// LookupToken 供 GET /file/{token} 使用。
func (d *Downloader) LookupToken(token string) (*Result, error) {
	e, err := d.cache.GetByToken(token)
	if err != nil {
		return nil, err
	}
	if err := d.validateCachedEntry(context.Background(), e, e.Format); err != nil {
		if errors.Is(err, ErrInvalidMedia) {
			return nil, &NotFoundError{Reason: "cached media invalid"}
		}
		return nil, err
	}
	return d.resultFromEntry(e, true), nil
}

// validateCachedEntry drops invalid cached media (especially audio-only mp4).
func (d *Downloader) validateCachedEntry(ctx context.Context, e CacheEntry, format string) error {
	if d == nil || d.cfg == nil || d.cache == nil {
		return fmt.Errorf("download: nil downloader")
	}
	err := validateMediaFile(ctx, d.probe, d.cfg.FFmpegLocation, e.Path, format)
	if err == nil || !errors.Is(err, ErrInvalidMedia) {
		return err
	}
	_, invalidateErr := d.cache.Invalidate(e)
	return errors.Join(err, invalidateErr)
}

func (d *Downloader) downloadOnce(ctx context.Context, req Request, videoID, format, bitrate string) (*Result, error) {
	// 限流：排队等待，而不是立刻 429（契约：超出排队；超时由 context 控制）。
	if err := d.sem.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("download: wait for slot: %w", err)
	}
	defer d.sem.Release(1)

	// 超时
	timeout := d.cfg.DownloadTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 再查一次缓存（等待信号量期间可能别人写好了）
	if e, ok := d.cache.Get(videoID, format, bitrate); ok {
		if err := d.validateCachedEntry(cctx, e, format); err == nil {
			return d.resultFromEntry(e, true), nil
		} else if !errors.Is(err, ErrInvalidMedia) {
			return nil, err
		}
	}

	finalName := videoID + "." + format
	finalPath := filepath.Join(d.cfg.DownloadDir, finalName)
	if err := ensureUnderDir(d.cfg.DownloadDir, finalPath); err != nil {
		return nil, err
	}

	// 临时文件：同目录，便于 atomic rename
	tmpPattern := videoID + ".*." + format + ".part"
	tmpFile, err := os.CreateTemp(d.cfg.DownloadDir, tmpPattern)
	if err != nil {
		return nil, fmt.Errorf("download: create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath) // yt-dlp 自己创建

	// yt-dlp 输出模板：用无扩展的 temp stem，让它补扩展名
	// 使用明确最终扩展：tempPath 直接作为输出（含扩展）
	// 但 CreateTemp 生成的名字可能含随机串，适合防冲突。
	// 为让 yt-dlp 写出到确切路径，用 --output tmpPath（无额外模板字段）。
	// 注意：extract-audio 时 yt-dlp 可能把扩展改成目标 format。
	outTemplate := tmpPath
	// 若 tmpPath 已有扩展，yt-dlp 可能再追加；用 stem + .%(ext)s 更稳
	stem := strings.TrimSuffix(tmpPath, filepath.Ext(tmpPath))
	// 去掉可能的 .part
	stem = strings.TrimSuffix(stem, ".part")
	outTemplate = stem + ".%(ext)s"

	// 清理可能残留
	cleanupGlobs := []string{stem + ".*"}
	defer func() {
		for _, g := range cleanupGlobs {
			matches, _ := filepath.Glob(g)
			for _, m := range matches {
				// 不要删 final
				if filepath.Clean(m) == filepath.Clean(finalPath) {
					continue
				}
				_ = os.Remove(m)
			}
		}
	}()

	ffmpegLoc := strings.TrimSpace(d.cfg.FFmpegLocation)
	// 若配置的是 ffmpeg.exe 路径，传其目录给 --ffmpeg-location 也可用；
	// yt-dlp 同时接受文件或目录。
	cookieFile := strings.TrimSpace(d.cfg.CookiesFile)
	// 每次下载前尝试把目录里新丢的 txt 提升到稳定文件（路径不变）。
	if d.cfg.CookiesDir != "" && cookieFile != "" {
		_ = cookies.RefreshDropIns(d.cfg.CookiesDir, cookieFile)
	}
	// yt-dlp rewrites --cookies in place. Always feed a snapshot so a real
	// login jar is not replaced with anonymous visitor cookies.
	cookieForYtdlp := cookieFile
	if cookieFile != "" {
		if snap, cleanup, serr := cookies.SnapshotForYtdlp(cookieFile); serr == nil && snap != "" {
			cookieForYtdlp = snap
			defer cleanup()
		}
	}
	opt := YtdlpOptions{
		YtdlpPath:      d.cfg.YtdlpPath,
		FFmpegLocation: ffmpegLoc,
		Proxy:          d.cfg.Proxy,
		CookiesFile:    cookieForYtdlp,
		Format:         format,
		Bitrate:        bitrate,
		OutputPath:     outTemplate,
		URL:            "https://www.youtube.com/watch?v=" + videoID,
		MaxFilesize:    d.cfg.MaxFilesizeBytes(),
	}

	if err := runYtdlp(cctx, d.runner, opt); err != nil {
		return nil, err
	}

	produced, err := findProducedFile(stem+"."+format, format)
	if err != nil {
		// 再试 stem 任意
		produced, err = findProducedFile(stem+".tmp", format)
		if err != nil {
			return nil, err
		}
	}

	st, err := os.Stat(produced)
	if err != nil {
		return nil, fmt.Errorf("download: stat produced: %w", err)
	}
	if st.Size() <= 0 {
		return nil, fmt.Errorf("download: empty output file")
	}
	maxBytes := d.cfg.MaxFilesizeBytes()
	if maxBytes > 0 && st.Size() > maxBytes {
		_ = os.Remove(produced)
		return nil, &TooLargeError{
			Size:     st.Size(),
			MaxBytes: maxBytes,
			VideoID:  videoID,
			Format:   format,
		}
	}
	if err := validateMediaFile(cctx, d.probe, d.cfg.FFmpegLocation, produced, format); err != nil {
		_ = os.Remove(produced)
		return nil, err
	}

	// 原子落盘
	if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
		// 忽略
	}
	if err := os.Rename(produced, finalPath); err != nil {
		// Windows 跨卷失败时 fallback copy
		data, rerr := os.ReadFile(produced)
		if rerr != nil {
			return nil, fmt.Errorf("download: rename: %v; read: %w", err, rerr)
		}
		if werr := os.WriteFile(finalPath, data, 0o644); werr != nil {
			return nil, fmt.Errorf("download: write final: %w", werr)
		}
		_ = os.Remove(produced)
	}

	// 防止 rename 后 defer 清掉 final：把 produced 标记已迁走
	cleanupGlobs = []string{stem + ".*"}

	finalStat, err := os.Stat(finalPath)
	if err != nil {
		return nil, err
	}

	artists := req.Artists
	if artists == nil {
		artists = []string{}
	} else {
		artists = append([]string(nil), artists...)
	}
	display := req.DisplayName
	if display == "" {
		display = req.Title
	}
	entry := CacheEntry{
		VideoID:     videoID,
		Format:      format,
		Bitrate:     bitrate,
		Path:        finalPath,
		Size:        finalStat.Size(),
		Title:       req.Title,
		Artists:     artists,
		DisplayName: display,
		DurationSec: req.DurationSeconds,
		CreatedAt:   d.now(),
	}
	entry.ExpiresAt = entry.CreatedAt.Add(d.cfg.CacheTTL)
	if err := d.cache.Put(entry); err != nil {
		return nil, err
	}
	// Put 会填 token
	if e, ok := d.cache.Get(videoID, format, bitrate); ok {
		entry = e
	}
	return d.resultFromEntry(entry, false), nil
}

func (d *Downloader) resultFromEntry(e CacheEntry, cached bool) *Result {
	ttl := d.cache.RemainingTTL(e)
	sec := int(ttl / time.Second)
	if sec < 1 && ttl > 0 {
		sec = 1
	}
	artists := e.Artists
	if artists == nil {
		artists = []string{}
	} else {
		artists = append([]string(nil), artists...)
	}
	filename := SanitizeFilename(e.Title)
	if filename == "audio" && e.DisplayName != "" {
		filename = SanitizeFilename(e.DisplayName)
	}
	if filename == "audio" {
		filename = e.VideoID
	}
	filename = filename + "." + e.Format
	return &Result{
		Path:            e.Path,
		Size:            e.Size,
		Cached:          cached,
		Token:           e.Token,
		Format:          e.Format,
		Bitrate:         e.Bitrate,
		VideoID:         e.VideoID,
		Title:           e.Title,
		Artists:         artists,
		DisplayName:     e.DisplayName,
		DurationSeconds: e.DurationSec,
		ExpiresIn:       sec,
		ContentType:     contentTypeFor(e.Format),
		Filename:        filename,
	}
}

func contentTypeFor(format string) string {
	switch strings.ToLower(format) {
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/mp4"
	case "opus":
		return "audio/ogg"
	case "mp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

// CacheDir returns the on-disk cache directory.
func (d *Downloader) CacheDir() string {
	return d.cfg.DownloadDir
}

// Cleanup removes expired cache entries and enforces CacheMaxTotalBytes.
// Returns cleanup stats for logging.
func (d *Downloader) Cleanup() (CleanupStats, error) {
	if d == nil || d.cache == nil {
		return CleanupStats{}, nil
	}
	maxBytes := int64(0)
	if d.cfg != nil {
		maxBytes = d.cfg.CacheMaxTotalBytes()
	}
	return d.cache.Cleanup(maxBytes)
}

// ResolveYtdlpPath resolves the configured yt-dlp path (or PATH / bin/).
func (d *Downloader) ResolveYtdlpPath() (string, error) {
	if d == nil || d.cfg == nil {
		return resolveYtdlpPath("")
	}
	return resolveYtdlpPath(d.cfg.YtdlpPath)
}
