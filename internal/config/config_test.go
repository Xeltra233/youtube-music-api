package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 8787 {
		t.Errorf("默认监听地址错误: %s", cfg.Addr())
	}
	if cfg.DefaultLimit != 10 {
		t.Errorf("DefaultLimit 期望 10，实际 %d", cfg.DefaultLimit)
	}
	if cfg.MaxLimit != 20 {
		t.Errorf("MaxLimit 期望 20，实际 %d", cfg.MaxLimit)
	}
	if cfg.MinScore != 0 {
		t.Errorf("MinScore 默认应为 0（不过滤），实际 %v", cfg.MinScore)
	}
	if cfg.AudioFormat != "mp3" || cfg.AudioBitrate != "192" {
		t.Errorf("音频默认值错误: %s/%s", cfg.AudioFormat, cfg.AudioBitrate)
	}
	if cfg.MaxFilesizeMB != 500 {
		t.Errorf("MaxFilesizeMB 默认应为 500（官方 MV），实际 %d", cfg.MaxFilesizeMB)
	}
	if !filepath.IsAbs(cfg.DownloadDir) {
		t.Errorf("DownloadDir 应为绝对路径，实际 %s", cfg.DownloadDir)
	}
	if cfg.AdminPassword != "" || cfg.AdminEnabled() {
		t.Errorf("默认不应启用 admin，password=%q", cfg.AdminPassword)
	}
	if cfg.AdminSessionTTL != 12*time.Hour {
		t.Errorf("AdminSessionTTL 默认应为 12h，实际 %v", cfg.AdminSessionTTL)
	}
	if cfg.CookiesFromBrowser != "" || cfg.HasBrowserCookieSource() {
		t.Errorf("默认不应启用浏览器 Cookie，同步源=%q", cfg.CookiesFromBrowser)
	}
	if !cfg.CookiesBrowserSyncOnStart {
		t.Error("浏览器 Cookie 启动同步默认应开启（仅配置来源后生效）")
	}
	if cfg.CookiesBrowserSyncEvery != 6*time.Hour {
		t.Errorf("浏览器 Cookie 周期默认应为 6h，实际 %v", cfg.CookiesBrowserSyncEvery)
	}
	if cfg.BrowserCookieStartupSyncEnabled() || cfg.BrowserCookiePeriodicSyncEnabled() {
		t.Error("没有浏览器来源时不应调度同步")
	}
}

func TestBrowserCookieConfigFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("COOKIES_FROM_BROWSER", `CHROME+GNOMEKEYRING:C:\Users\Example User\Profile 1`)
	t.Setenv("COOKIES_BROWSER_SYNC_ON_START", "false")
	t.Setenv("COOKIES_BROWSER_SYNC_INTERVAL_SECONDS", "900")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	want := `chrome+gnomekeyring:C:\Users\Example User\Profile 1`
	if cfg.CookiesFromBrowser != want {
		t.Fatalf("CookiesFromBrowser=%q want %q", cfg.CookiesFromBrowser, want)
	}
	if !cfg.HasBrowserCookieSource() {
		t.Fatal("配置浏览器 spec 后应启用同步源")
	}
	if cfg.CookiesBrowserSyncOnStart {
		t.Fatal("COOKIES_BROWSER_SYNC_ON_START=false 未生效")
	}
	if cfg.CookiesBrowserSyncEvery != 15*time.Minute {
		t.Fatalf("CookiesBrowserSyncEvery=%v", cfg.CookiesBrowserSyncEvery)
	}
	if cfg.BrowserCookieStartupSyncEnabled() {
		t.Fatal("显式关闭后不应调度启动同步")
	}
	if !cfg.BrowserCookiePeriodicSyncEnabled() {
		t.Fatal("正数周期应启用周期同步")
	}
}

func TestNormalizeCookiesFromBrowserSpecVariants(t *testing.T) {
	cases := map[string]string{
		"chrome":             "chrome",
		"CHROMIUM:Profile 1": "chromium:Profile 1",
		`CHROME+KWALLET6:C:\Profiles\YouTube Account`: `chrome+kwallet6:C:\Profiles\YouTube Account`,
		"firefox:default-release::none":               "firefox:default-release::none",
		"safari":                                      "safari",
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			got, err := normalizeCookiesFromBrowserSpec(raw)
			if err != nil {
				t.Fatalf("normalize 失败: %v", err)
			}
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}

func TestInvalidCookiesFromBrowserSpecs(t *testing.T) {
	cases := []string{
		"ie",
		"chrome+dpapi",
		"chrome+basictext+kwallet",
		"firefox+kwallet",
		":Default",
		"chrome :Default",
		"chrome\t:Default",
		"chrome:Default\u0007",
		"chrome:Default\n--proxy=http://example.invalid",
		"+kwallet",
		strings.Repeat("a", maxCookiesFromBrowserSpecBytes+1),
	}
	for _, raw := range cases {
		t.Run(strings.ReplaceAll(raw, "\n", `\n`), func(t *testing.T) {
			if _, err := normalizeCookiesFromBrowserSpec(raw); err == nil {
				t.Fatalf("非法 spec %q 应报错", raw)
			}
		})
	}
}

func TestBrowserCookieSyncIntervalFloorAndDisable(t *testing.T) {
	t.Run("positive_value_has_one_minute_floor", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("COOKIES_FROM_BROWSER", "chrome")
		t.Setenv("COOKIES_BROWSER_SYNC_INTERVAL_SECONDS", "1")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load 失败: %v", err)
		}
		if cfg.CookiesBrowserSyncEvery != time.Minute {
			t.Fatalf("同步周期下限应为 1m，实际 %v", cfg.CookiesBrowserSyncEvery)
		}
	})
	t.Run("zero_disables_periodic_sync", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("COOKIES_FROM_BROWSER", "firefox:default-release")
		t.Setenv("COOKIES_BROWSER_SYNC_INTERVAL_SECONDS", "0")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load 失败: %v", err)
		}
		if cfg.CookiesBrowserSyncEvery != 0 {
			t.Fatalf("0 应关闭周期同步，实际 %v", cfg.CookiesBrowserSyncEvery)
		}
		if cfg.BrowserCookiePeriodicSyncEnabled() {
			t.Fatal("周期为 0 时不应调度周期同步")
		}
		if !cfg.BrowserCookieStartupSyncEnabled() {
			t.Fatal("周期为 0 不应关闭默认启动同步")
		}
	})
}

// R5：配置不得把上限压到 20 以下，服务端起码要能返回 20 条。
func TestMaxLimitFloorIs20(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAX_LIMIT", "5")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.MaxLimit != 20 {
		t.Errorf("MAX_LIMIT=5 应被抬到 20，实际 %d", cfg.MaxLimit)
	}
}

func TestMaxLimitCanExceed20(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAX_LIMIT", "50")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.MaxLimit != 50 {
		t.Errorf("MAX_LIMIT=50 应保留，实际 %d", cfg.MaxLimit)
	}
}

func TestDefaultLimitClampedToMaxLimit(t *testing.T) {
	clearEnv(t)
	t.Setenv("DEFAULT_LIMIT", "99")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.DefaultLimit != cfg.MaxLimit {
		t.Errorf("DEFAULT_LIMIT=99 应夹到 MaxLimit=%d，实际 %d", cfg.MaxLimit, cfg.DefaultLimit)
	}
}

func TestResolveLimit(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	cases := []struct {
		requested int
		want      int
		reason    string
	}{
		{0, 10, "未指定 → 默认 10"},
		{-3, 10, "负数 → 默认 10"},
		{1, 1, "1 → 1"},
		{10, 10, "10 → 10"},
		{20, 20, "20 → 20（最大值必须支持）"},
		{25, 20, "25 → 夹到 20"},
		{1000, 20, "1000 → 夹到 20"},
	}
	for _, tc := range cases {
		if got := cfg.ResolveLimit(tc.requested); got != tc.want {
			t.Errorf("%s: ResolveLimit(%d)=%d, 期望 %d", tc.reason, tc.requested, got, tc.want)
		}
	}
}

func TestResolveMinScore(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIN_SCORE", "0.3")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got := cfg.ResolveMinScore(-1); got != 0.3 {
		t.Errorf("未指定应回落到配置 0.3，实际 %v", got)
	}
	if got := cfg.ResolveMinScore(0.8); got != 0.8 {
		t.Errorf("请求参数应优先，实际 %v", got)
	}
	if got := cfg.ResolveMinScore(5); got != 1 {
		t.Errorf(">1 应夹到 1，实际 %v", got)
	}
	if got := cfg.ResolveMinScore(0); got != 0 {
		t.Errorf("显式 0 应为 0（不过滤），实际 %v", got)
	}
}

func TestInvalidValues(t *testing.T) {
	t.Run("bitrate", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("AUDIO_BITRATE", "abc")
		if _, err := Load(""); err == nil {
			t.Error("非法 AUDIO_BITRATE 应报错")
		}
	})
	t.Run("format", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("AUDIO_FORMAT", "flac")
		if _, err := Load(""); err == nil {
			t.Error("不支持的 AUDIO_FORMAT 应报错")
		}
	})
	t.Run("port", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("PORT", "70000")
		if _, err := Load(""); err == nil {
			t.Error("非法 PORT 应报错")
		}
	})
	t.Run("min_score", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("MIN_SCORE", "1.5")
		if _, err := Load(""); err == nil {
			t.Error("越界 MIN_SCORE 应报错")
		}
	})
	t.Run("browser_spec", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("COOKIES_FROM_BROWSER", "netscape:Default")
		if _, err := Load(""); err == nil {
			t.Error("不支持的 COOKIES_FROM_BROWSER 应报错")
		}
	})
	t.Run("browser_sync_bool", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("COOKIES_BROWSER_SYNC_ON_START", "sometimes")
		if _, err := Load(""); err == nil {
			t.Error("非法 COOKIES_BROWSER_SYNC_ON_START 应报错")
		}
	})
	t.Run("browser_sync_negative_interval", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("COOKIES_BROWSER_SYNC_INTERVAL_SECONDS", "-1")
		if _, err := Load(""); err == nil {
			t.Error("负数 COOKIES_BROWSER_SYNC_INTERVAL_SECONDS 应报错")
		}
	})
}

// 数值项写错必须 fail fast，不能静默回落默认值（否则用户改了配置却毫无感知）。
func TestNonNumericValuesFailFast(t *testing.T) {
	cases := map[string]string{
		"PORT":                                  "abc",
		"DEFAULT_LIMIT":                         "十",
		"MAX_LIMIT":                             "20x",
		"MIN_SCORE":                             "high",
		"SESSION_TTL_SECONDS":                   "5m",
		"CACHE_TTL_SECONDS":                     "-1",
		"COOKIES_BROWSER_SYNC_INTERVAL_SECONDS": "5m",
	}
	for key, bad := range cases {
		t.Run(key, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(key, bad)
			if _, err := Load(""); err == nil {
				t.Errorf("%s=%q 应报错而非静默回落", key, bad)
			}
		})
	}
}

func TestPortExpandsTemplateAndAliases(t *testing.T) {
	t.Run("expand_WEB_PORT_template", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_PORT", "18080")
		t.Setenv("PORT", "${WEB_PORT}")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load 失败: %v", err)
		}
		if cfg.Port != 18080 {
			t.Fatalf("PORT=${WEB_PORT} 应展开为 18080，实际 %d", cfg.Port)
		}
	})
	t.Run("fallback_WEB_PORT_when_PORT_unresolved", func(t *testing.T) {
		clearEnv(t)
		// WEB_PORT 本身也没有时，未展开模板应回落默认，而不是把进程打挂。
		t.Setenv("PORT", "${WEB_PORT}")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("未展开 PORT 模板不应致命: %v", err)
		}
		if cfg.Port != 8787 {
			t.Fatalf("应回落默认 8787，实际 %d", cfg.Port)
		}
	})
	t.Run("alias_WEB_PORT", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_PORT", "9090")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load 失败: %v", err)
		}
		if cfg.Port != 9090 {
			t.Fatalf("仅 WEB_PORT 时应使用 9090，实际 %d", cfg.Port)
		}
	})
	t.Run("plain_PORT_still_wins", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("PORT", "8081")
		t.Setenv("WEB_PORT", "9090")
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load 失败: %v", err)
		}
		if cfg.Port != 8081 {
			t.Fatalf("显式 PORT 应优先，实际 %d", cfg.Port)
		}
	})
}

func TestBlankHostFallsBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOST", "   ")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("空白 HOST 应回落 127.0.0.1，实际 %q", cfg.Host)
	}
}

func TestBitrateWithKSuffix(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUDIO_BITRATE", "320k")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.AudioBitrate != "320" {
		t.Errorf("320k 应归一化为 320，实际 %q", cfg.AudioBitrate)
	}
}

func TestEnvFileAndPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# 注释行\nPORT=9001\nDEFAULT_LIMIT=7\nAPI_KEY=\"secret-from-file\"\n\nBROKENLINE\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Port != 9001 || cfg.DefaultLimit != 7 || cfg.APIKey != "secret-from-file" {
		t.Errorf(".env 未生效: port=%d limit=%d key=%q", cfg.Port, cfg.DefaultLimit, cfg.APIKey)
	}

	// 环境变量优先级应高于 .env
	t.Setenv("PORT", "9002")
	cfg2, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg2.Port != 9002 {
		t.Errorf("环境变量应覆盖 .env，实际 port=%d", cfg2.Port)
	}
}

func TestEnvFileMissingIsOK(t *testing.T) {
	clearEnv(t)
	if _, err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf(".env 不存在时不应报错，实际 %v", err)
	}
}

func TestDurationAndFloorValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("SESSION_TTL_SECONDS", "1")
	t.Setenv("DOWNLOAD_TIMEOUT_SECONDS", "1")
	t.Setenv("MAX_CONCURRENT_DOWNLOADS", "0")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.SessionTTL != 30*time.Second {
		t.Errorf("SessionTTL 下限应为 30s，实际 %v", cfg.SessionTTL)
	}
	if cfg.DownloadTimeout != 10*time.Second {
		t.Errorf("DownloadTimeout 下限应为 10s，实际 %v", cfg.DownloadTimeout)
	}
	if cfg.MaxConcurrentDownloads != 1 {
		t.Errorf("MaxConcurrentDownloads 下限应为 1，实际 %d", cfg.MaxConcurrentDownloads)
	}
}

func TestByteHelpers(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAX_FILESIZE_MB", "3")
	t.Setenv("CACHE_MAX_TOTAL_MB", "64")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.MaxFilesizeBytes() != 3*1024*1024 {
		t.Errorf("MaxFilesizeBytes 错误: %d", cfg.MaxFilesizeBytes())
	}
	if cfg.CacheMaxTotalBytes() != 64*1024*1024 {
		t.Errorf("CacheMaxTotalBytes 错误: %d", cfg.CacheMaxTotalBytes())
	}
}

func TestAdminConfigFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ADMIN_PASSWORD", "s3cret")
	t.Setenv("ADMIN_SESSION_SECRET", "sess-secret")
	t.Setenv("ADMIN_SESSION_TTL_SECONDS", "600")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if !cfg.AdminEnabled() {
		t.Fatal("设置 ADMIN_PASSWORD 后应启用 admin")
	}
	if cfg.AdminPassword != "s3cret" {
		t.Fatalf("AdminPassword=%q", cfg.AdminPassword)
	}
	if cfg.AdminSessionSecret != "sess-secret" {
		t.Fatalf("AdminSessionSecret=%q", cfg.AdminSessionSecret)
	}
	if cfg.AdminSessionTTL != 10*time.Minute {
		t.Fatalf("AdminSessionTTL=%v", cfg.AdminSessionTTL)
	}
}

func TestAdminSessionTTLFloor(t *testing.T) {
	clearEnv(t)
	t.Setenv("ADMIN_PASSWORD", "x")
	t.Setenv("ADMIN_SESSION_TTL_SECONDS", "30") // < 5m
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.AdminSessionTTL != 5*time.Minute {
		t.Fatalf("TTL 下限应为 5m，实际 %v", cfg.AdminSessionTTL)
	}
}

// clearEnv 清空本包会读取的环境变量，保证用例之间互不干扰。
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"HOST", "PORT", "WEB_PORT", "HTTP_PORT", "API_KEY",
		"ADMIN_PASSWORD", "ADMIN_SESSION_SECRET", "ADMIN_SESSION_TTL_SECONDS",
		"DEFAULT_LIMIT", "MAX_LIMIT", "MIN_SCORE",
		"DOWNLOAD_DIR", "AUDIO_FORMAT", "AUDIO_BITRATE", "FFMPEG_LOCATION", "YTDLP_PATH",
		"PROXY", "COOKIES_FILE", "COOKIES_DIR", "COOKIES_KEEPALIVE", "COOKIES_KEEPALIVE_INTERVAL_SECONDS",
		"COOKIES_FROM_BROWSER", "COOKIES_BROWSER_SYNC_ON_START", "COOKIES_BROWSER_SYNC_INTERVAL_SECONDS",
		"MAX_CONCURRENT_DOWNLOADS", "MAX_FILESIZE_MB",
		"DOWNLOAD_TIMEOUT_SECONDS", "SESSION_TTL_SECONDS", "CACHE_TTL_SECONDS",
		"CACHE_MAX_TOTAL_MB", "CLEANUP_INTERVAL_SECONDS", "SEARCH_TIMEOUT_SECONDS",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}
