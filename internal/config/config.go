// Package config 负责从环境变量 / .env 文件加载服务配置。
//
// 设计约束（对应需求 R4/R5）：DefaultLimit 与 MaxLimit 只提供「默认值」和「硬上限」，
// 单次请求要多少条由 bot 通过请求参数决定；服务端必须保证至少支持 20 条。
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// MinimumMaxLimit 是 MaxLimit 的下限：用户要求「最大 20，你起码要支持返回那么多」。
const MinimumMaxLimit = 20

// Config 是服务端全部可调项。
type Config struct {
	// HTTP
	Host   string
	Port   int
	APIKey string

	// Admin 上传面板（独立于 bot API_KEY）
	// AdminPassword 为空时不启用 /admin 与 /api/admin。
	AdminPassword      string
	AdminSessionSecret string
	AdminSessionTTL    time.Duration

	// 搜索
	DefaultLimit int
	MaxLimit     int
	MinScore     float64

	// 下载
	DownloadDir    string
	AudioFormat    string
	AudioBitrate   string
	FFmpegLocation string
	YtdlpPath      string
	Proxy          string
	CookiesFile    string
	CookiesDir     string
	// CookiesFromBrowser is one complete yt-dlp browser spec:
	// BROWSER[+KEYRING][:PROFILE][::CONTAINER]. It is passed as one argv value.
	CookiesFromBrowser        string
	CookiesBrowserSyncOnStart bool
	CookiesBrowserSyncEvery   time.Duration // 0 disables periodic sync
	CookieSourceMode          string        // auto | managed | external | file
	YouTubeLoginBrowserPath   string
	YouTubeLoginProfileDir    string
	YouTubeLoginHeadless      bool
	YouTubeLoginSessionTTL    time.Duration
	YouTubeLoginRefreshEvery  time.Duration // 0 disables periodic managed refresh
	CookiesKeepAlive          bool
	CookiesKeepAliveEvery     time.Duration
	MaxConcurrentDownloads    int
	MaxFilesizeMB             int
	DownloadTimeout           time.Duration

	// 生命周期
	SessionTTL      time.Duration
	CacheTTL        time.Duration
	CacheMaxTotalMB int
	CleanupInterval time.Duration
	SearchTimeout   time.Duration
}

var validAudioFormats = map[string]bool{"mp3": true, "m4a": true, "opus": true}

var validCookieBrowsers = map[string]bool{
	"brave": true, "chrome": true, "chromium": true, "edge": true,
	"firefox": true, "opera": true, "safari": true, "vivaldi": true, "whale": true,
}

var validCookieKeyrings = map[string]bool{
	"basictext": true, "gnomekeyring": true, "kwallet": true,
	"kwallet5": true, "kwallet6": true,
}

var chromiumCookieBrowsers = map[string]bool{
	"brave": true, "chrome": true, "chromium": true, "edge": true,
	"opera": true, "vivaldi": true, "whale": true,
}

var validCookieSourceModes = map[string]bool{
	"auto": true, "managed": true, "external": true, "file": true,
}

const (
	defaultCookiesBrowserSyncEvery = 6 * time.Hour
	minimumCookiesBrowserSyncEvery = time.Minute
	defaultYouTubeLoginSessionTTL  = 15 * time.Minute
	defaultYouTubeLoginRefresh     = 6 * time.Hour
	maxCookiesFromBrowserSpecBytes = 4096
)

// Load 读取 .env（若存在）与环境变量，返回校验后的配置。
// 环境变量优先级高于 .env 文件。数值项写错时直接报错（fail fast），不静默回落默认值。
func Load(envFile string) (*Config, error) {
	fileVals, err := parseEnvFile(envFile)
	if err != nil {
		return nil, err
	}
	l := &loader{file: fileVals}

	cfg := &Config{
		Host:                      l.str("HOST", "127.0.0.1"),
		Port:                      l.port(8787),
		APIKey:                    l.str("API_KEY", ""),
		AdminPassword:             l.str("ADMIN_PASSWORD", ""),
		AdminSessionSecret:        l.str("ADMIN_SESSION_SECRET", ""),
		AdminSessionTTL:           l.seconds("ADMIN_SESSION_TTL_SECONDS", 12*time.Hour),
		DefaultLimit:              l.int("DEFAULT_LIMIT", 10),
		MaxLimit:                  l.int("MAX_LIMIT", MinimumMaxLimit),
		MinScore:                  l.float("MIN_SCORE", 0.0),
		DownloadDir:               l.str("DOWNLOAD_DIR", "downloads"),
		AudioFormat:               strings.ToLower(l.str("AUDIO_FORMAT", "mp3")),
		AudioBitrate:              l.str("AUDIO_BITRATE", "192"),
		FFmpegLocation:            l.str("FFMPEG_LOCATION", ""),
		YtdlpPath:                 l.str("YTDLP_PATH", ""),
		Proxy:                     l.str("PROXY", ""),
		CookiesFile:               l.str("COOKIES_FILE", ""),
		CookiesDir:                l.str("COOKIES_DIR", "cookies"),
		CookiesFromBrowser:        l.str("COOKIES_FROM_BROWSER", ""),
		CookiesBrowserSyncOnStart: l.bool("COOKIES_BROWSER_SYNC_ON_START", true),
		CookiesBrowserSyncEvery: l.seconds(
			"COOKIES_BROWSER_SYNC_INTERVAL_SECONDS",
			defaultCookiesBrowserSyncEvery,
		),
		CookieSourceMode:         strings.ToLower(l.str("COOKIE_SOURCE_MODE", "auto")),
		YouTubeLoginBrowserPath:  l.str("YOUTUBE_LOGIN_BROWSER_PATH", ""),
		YouTubeLoginProfileDir:   l.str("YOUTUBE_LOGIN_PROFILE_DIR", "browser-profile"),
		YouTubeLoginHeadless:     l.bool("YOUTUBE_LOGIN_HEADLESS", true),
		YouTubeLoginSessionTTL:   l.seconds("YOUTUBE_LOGIN_SESSION_TTL_SECONDS", defaultYouTubeLoginSessionTTL),
		YouTubeLoginRefreshEvery: l.seconds("YOUTUBE_LOGIN_REFRESH_INTERVAL_SECONDS", defaultYouTubeLoginRefresh),
		CookiesKeepAlive:         l.bool("COOKIES_KEEPALIVE", false),
		CookiesKeepAliveEvery:    l.seconds("COOKIES_KEEPALIVE_INTERVAL_SECONDS", 6*time.Hour),
		MaxConcurrentDownloads:   l.int("MAX_CONCURRENT_DOWNLOADS", 2),
		// Official music videos are often >50MB; default high enough for common 1080p MVs.
		MaxFilesizeMB:   l.int("MAX_FILESIZE_MB", 500),
		DownloadTimeout: l.seconds("DOWNLOAD_TIMEOUT_SECONDS", 300*time.Second),
		SessionTTL:      l.seconds("SESSION_TTL_SECONDS", 30*time.Minute),
		CacheTTL:        l.seconds("CACHE_TTL_SECONDS", 24*time.Hour),
		CacheMaxTotalMB: l.int("CACHE_MAX_TOTAL_MB", 2048),
		CleanupInterval: l.seconds("CLEANUP_INTERVAL_SECONDS", 5*time.Minute),
		SearchTimeout:   l.seconds("SEARCH_TIMEOUT_SECONDS", 15*time.Second),
	}
	if err := l.err(); err != nil {
		return nil, err
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// normalize 校验并修正配置，保证不变量成立。
func (c *Config) normalize() error {
	c.Host = strings.TrimSpace(c.Host)
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT 必须在 1-65535 之间，当前 %d", c.Port)
	}

	// R5：服务端必须至少支持返回 20 条，配置不得把上限压到 20 以下。
	if c.MaxLimit < MinimumMaxLimit {
		c.MaxLimit = MinimumMaxLimit
	}
	if c.DefaultLimit < 1 {
		c.DefaultLimit = 1
	}
	if c.DefaultLimit > c.MaxLimit {
		c.DefaultLimit = c.MaxLimit
	}

	if c.MinScore < 0 || c.MinScore > 1 {
		return fmt.Errorf("MIN_SCORE 必须在 0.0-1.0 之间，当前 %v", c.MinScore)
	}

	if !validAudioFormats[c.AudioFormat] {
		return fmt.Errorf("AUDIO_FORMAT 只支持 mp3/m4a/opus，当前 %q", c.AudioFormat)
	}

	bitrate := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(c.AudioBitrate), "k"), "K")
	if _, err := strconv.Atoi(bitrate); err != nil {
		return fmt.Errorf("AUDIO_BITRATE 必须是数字（kbps），当前 %q", c.AudioBitrate)
	}
	c.AudioBitrate = bitrate

	if c.MaxConcurrentDownloads < 1 {
		c.MaxConcurrentDownloads = 1
	}
	if c.MaxFilesizeMB < 1 {
		c.MaxFilesizeMB = 1
	}
	if c.CacheMaxTotalMB < 16 {
		c.CacheMaxTotalMB = 16
	}
	if c.DownloadTimeout < 10*time.Second {
		c.DownloadTimeout = 10 * time.Second
	}
	if c.SearchTimeout < 3*time.Second {
		c.SearchTimeout = 3 * time.Second
	}
	if c.SessionTTL < 30*time.Second {
		c.SessionTTL = 30 * time.Second
	}
	if c.CacheTTL < time.Minute {
		c.CacheTTL = time.Minute
	}
	if c.CleanupInterval < 10*time.Second {
		c.CleanupInterval = 10 * time.Second
	}

	c.CookiesFile = strings.TrimSpace(c.CookiesFile)
	c.CookiesDir = strings.TrimSpace(c.CookiesDir)
	c.CookieSourceMode = strings.ToLower(strings.TrimSpace(c.CookieSourceMode))
	if c.CookieSourceMode == "" {
		c.CookieSourceMode = "auto"
	}
	if !validCookieSourceModes[c.CookieSourceMode] {
		return fmt.Errorf("COOKIE_SOURCE_MODE 只支持 auto/managed/external/file，当前 %q", c.CookieSourceMode)
	}
	browserSpec, err := normalizeCookiesFromBrowserSpec(c.CookiesFromBrowser)
	if err != nil {
		return fmt.Errorf("COOKIES_FROM_BROWSER 非法: %w", err)
	}
	c.CookiesFromBrowser = browserSpec
	if c.CookiesDir == "" {
		c.CookiesDir = "cookies"
	}
	if c.CookiesBrowserSyncEvery > 0 && c.CookiesBrowserSyncEvery < minimumCookiesBrowserSyncEvery {
		c.CookiesBrowserSyncEvery = minimumCookiesBrowserSyncEvery
	}
	c.YouTubeLoginBrowserPath = strings.TrimSpace(c.YouTubeLoginBrowserPath)
	c.YouTubeLoginProfileDir = strings.TrimSpace(c.YouTubeLoginProfileDir)
	if c.YouTubeLoginProfileDir == "" {
		c.YouTubeLoginProfileDir = "browser-profile"
	}
	if c.YouTubeLoginSessionTTL < time.Minute {
		c.YouTubeLoginSessionTTL = time.Minute
	}
	if c.YouTubeLoginSessionTTL > 30*time.Minute {
		c.YouTubeLoginSessionTTL = 30 * time.Minute
	}
	if c.YouTubeLoginRefreshEvery > 0 && c.YouTubeLoginRefreshEvery < time.Minute {
		c.YouTubeLoginRefreshEvery = time.Minute
	}
	if c.CookiesKeepAliveEvery < time.Minute {
		c.CookiesKeepAliveEvery = time.Minute
	}

	c.AdminPassword = strings.TrimSpace(c.AdminPassword)
	c.AdminSessionSecret = strings.TrimSpace(c.AdminSessionSecret)
	if c.AdminSessionTTL < 5*time.Minute {
		c.AdminSessionTTL = 5 * time.Minute
	}
	if c.AdminSessionTTL > 7*24*time.Hour {
		c.AdminSessionTTL = 7 * 24 * time.Hour
	}

	abs, err := filepath.Abs(c.DownloadDir)
	if err != nil {
		return fmt.Errorf("DOWNLOAD_DIR 无法解析为绝对路径: %w", err)
	}
	c.DownloadDir = abs

	if absDir, err := filepath.Abs(c.CookiesDir); err == nil {
		c.CookiesDir = absDir
	}
	profileDir, err := filepath.Abs(c.YouTubeLoginProfileDir)
	if err != nil {
		return fmt.Errorf("YOUTUBE_LOGIN_PROFILE_DIR 无法解析为绝对路径: %w", err)
	}
	c.YouTubeLoginProfileDir = profileDir
	return nil
}

// AdminEnabled 表示管理上传端是否可用。
func (c *Config) AdminEnabled() bool {
	return c != nil && strings.TrimSpace(c.AdminPassword) != ""
}

// HasBrowserCookieSource reports whether a browser profile source is configured.
func (c *Config) HasBrowserCookieSource() bool {
	return c != nil && strings.TrimSpace(c.CookiesFromBrowser) != ""
}

// BrowserCookieStartupSyncEnabled reports whether startup should run one sync.
func (c *Config) BrowserCookieStartupSyncEnabled() bool {
	return c.HasBrowserCookieSource() && c.CookiesBrowserSyncOnStart &&
		(c.cookieSourceMode() == "auto" || c.cookieSourceMode() == "external")
}

// BrowserCookiePeriodicSyncEnabled reports whether the periodic loop is enabled.
func (c *Config) BrowserCookiePeriodicSyncEnabled() bool {
	return c.HasBrowserCookieSource() && c.CookiesBrowserSyncEvery > 0 &&
		(c.cookieSourceMode() == "auto" || c.cookieSourceMode() == "external")
}

// ManagedCookieSourceEnabled reports whether the managed browser route is
// available as a configured source option.
func (c *Config) ManagedCookieSourceEnabled() bool {
	return c != nil && (c.cookieSourceMode() == "auto" || c.cookieSourceMode() == "managed")
}

func (c *Config) cookieSourceMode() string {
	if c == nil || strings.TrimSpace(c.CookieSourceMode) == "" {
		return "auto"
	}
	return strings.ToLower(strings.TrimSpace(c.CookieSourceMode))
}

// Addr 返回 http.Server 监听地址。
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// MaxFilesizeBytes 返回单文件体积上限（字节）。
func (c *Config) MaxFilesizeBytes() int64 {
	return int64(c.MaxFilesizeMB) * 1024 * 1024
}

// CacheMaxTotalBytes 返回缓存目录总量上限（字节）。
func (c *Config) CacheMaxTotalBytes() int64 {
	return int64(c.CacheMaxTotalMB) * 1024 * 1024
}

// EnsureDownloadDir 创建下载目录。
func (c *Config) EnsureDownloadDir() error {
	if err := os.MkdirAll(c.DownloadDir, 0o755); err != nil {
		return fmt.Errorf("创建下载目录 %s 失败: %w", c.DownloadDir, err)
	}
	return nil
}

// ResolveLimit 把 bot 请求的条数收敛到 [1, MaxLimit]。
// requested <= 0 表示 bot 没指定，使用服务端默认值。
func (c *Config) ResolveLimit(requested int) int {
	if requested <= 0 {
		return c.DefaultLimit
	}
	if requested > c.MaxLimit {
		return c.MaxLimit
	}
	return requested
}

// ResolveMinScore 请求参数优先，负数表示未指定。
func (c *Config) ResolveMinScore(requested float64) float64 {
	if requested < 0 {
		return c.MinScore
	}
	if requested > 1 {
		return 1
	}
	return requested
}

// normalizeCookiesFromBrowserSpec validates the yt-dlp grammar prefix while
// preserving PROFILE/CONTAINER verbatim (including spaces and Windows paths).
// The returned string remains one argument; callers must not split it.
func normalizeCookiesFromBrowserSpec(raw string) (string, error) {
	spec := strings.TrimSpace(raw)
	if spec == "" {
		return "", nil
	}
	if len(spec) > maxCookiesFromBrowserSpecBytes {
		return "", fmt.Errorf("长度超过 %d 字节", maxCookiesFromBrowserSpecBytes)
	}
	for _, r := range spec {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("包含控制字符")
		}
	}

	head, tail, hasTail := strings.Cut(spec, ":")
	if head == "" || strings.TrimSpace(head) != head || strings.ContainsAny(head, " ") {
		return "", fmt.Errorf("浏览器前缀为空或包含空白")
	}
	browser, keyring, hasKeyring := strings.Cut(head, "+")
	if strings.Contains(keyring, "+") {
		return "", fmt.Errorf("只能指定一个 keyring")
	}
	browser = strings.ToLower(browser)
	if !validCookieBrowsers[browser] {
		return "", fmt.Errorf("不支持的浏览器 %q", browser)
	}

	normalizedHead := browser
	if hasKeyring {
		keyring = strings.ToLower(keyring)
		if !validCookieKeyrings[keyring] {
			return "", fmt.Errorf("不支持的 keyring %q", keyring)
		}
		if !chromiumCookieBrowsers[browser] {
			return "", fmt.Errorf("浏览器 %q 不使用 Chromium keyring", browser)
		}
		normalizedHead += "+" + keyring
	}
	if hasTail {
		return normalizedHead + ":" + tail, nil
	}
	return normalizedHead, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	if path == "" {
		return values, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return values, nil
}

// loader 从「环境变量 > .env > 默认值」三级来源取值，并累积解析错误。
type loader struct {
	file   map[string]string
	errors []string
}

func (l *loader) lookup(key string) (string, bool) {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return expandSimpleRefs(strings.TrimSpace(v)), true
	}
	if v, ok := l.file[key]; ok && strings.TrimSpace(v) != "" {
		return expandSimpleRefs(strings.TrimSpace(v)), true
	}
	return "", false
}

func (l *loader) str(key, fallback string) string {
	if v, ok := l.lookup(key); ok {
		return v
	}
	return fallback
}

func (l *loader) bool(key string, fallback bool) bool {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off", "n":
		return false
	default:
		l.errors = append(l.errors, fmt.Sprintf("%s 必须是布尔值（1/0/true/false），当前 %q", key, v))
		return fallback
	}
}

func (l *loader) int(key string, fallback int) int {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		l.errors = append(l.errors, fmt.Sprintf("%s 必须是整数，当前 %q", key, v))
		return fallback
	}
	return parsed
}

// port 解析监听端口：
// 1) PORT
// 2) 常见平台别名 WEB_PORT / HTTP_PORT
// 3) 默认值
// 支持把字面量 "${WEB_PORT}" / "$WEB_PORT" 展开成真实环境变量。
// 若仍是未展开模板，则继续尝试别名，而不是直接把容器打挂。
func (l *loader) port(fallback int) int {
	keys := []string{"PORT", "WEB_PORT", "HTTP_PORT"}
	var lastBad string
	for _, key := range keys {
		raw, ok := rawLookup(l.file, key)
		if !ok {
			continue
		}
		expanded := strings.TrimSpace(expandSimpleRefs(raw))
		if expanded == "" || looksUnresolvedRef(expanded) {
			lastBad = raw
			continue
		}
		parsed, err := strconv.Atoi(expanded)
		if err != nil {
			l.errors = append(l.errors, fmt.Sprintf("%s 必须是整数，当前 %q", key, expanded))
			return fallback
		}
		return parsed
	}
	if lastBad != "" && looksUnresolvedRef(strings.TrimSpace(expandSimpleRefs(lastBad))) {
		// 平台把 PORT 写成了未展开模板，且别名也没有可用整数时，回落默认端口。
		return fallback
	}
	return fallback
}

func rawLookup(file map[string]string, key string) (string, bool) {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), true
	}
	if file != nil {
		if v, ok := file[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// expandSimpleRefs 展开 ${VAR} 与 $VAR（仅一层常见模板，足够处理平台变量转发）。
func expandSimpleRefs(s string) string {
	if s == "" || !strings.ContainsAny(s, "$") {
		return s
	}
	out := s
	for i := 0; i < 5 && strings.Contains(out, "$"); i++ {
		next := os.Expand(out, func(key string) string {
			if key == "" {
				return ""
			}
			if v, ok := os.LookupEnv(key); ok {
				return strings.TrimSpace(v)
			}
			return ""
		})
		if next == out {
			break
		}
		out = next
	}
	return strings.TrimSpace(out)
}

func looksUnresolvedRef(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "${") && strings.Contains(s, "}") {
		return true
	}
	// 纯 $WEB_PORT 这种未展开形式
	if strings.HasPrefix(s, "$") && !strings.ContainsAny(s, " \t") {
		name := strings.TrimPrefix(s, "$")
		name = strings.TrimPrefix(name, "{")
		name = strings.TrimSuffix(name, "}")
		if name != "" {
			if _, err := strconv.Atoi(name); err != nil {
				return true
			}
		}
	}
	return false
}

func (l *loader) float(key string, fallback float64) float64 {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		l.errors = append(l.errors, fmt.Sprintf("%s 必须是小数，当前 %q", key, v))
		return fallback
	}
	return parsed
}

func (l *loader) seconds(key string, fallback time.Duration) time.Duration {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		l.errors = append(l.errors, fmt.Sprintf("%s 必须是整数秒数，当前 %q", key, v))
		return fallback
	}
	if parsed < 0 {
		l.errors = append(l.errors, fmt.Sprintf("%s 不能为负数，当前 %d", key, parsed))
		return fallback
	}
	return time.Duration(parsed) * time.Second
}

func (l *loader) err() error {
	if len(l.errors) == 0 {
		return nil
	}
	return fmt.Errorf("配置校验失败: %s", strings.Join(l.errors, "; "))
}
