package download

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheCleanupExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	c, err := newCache(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }

	// Live entry.
	livePath := filepath.Join(dir, "live.mp3")
	if err := os.WriteFile(livePath, []byte("live-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(CacheEntry{
		VideoID: "liveid1", Format: "mp3", Bitrate: "192",
		Path: livePath, Size: 9, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Expired entry (TTL=1s semantics via explicit ExpiresAt).
	expPath := filepath.Join(dir, "expired.mp3")
	if err := os.WriteFile(expPath, []byte("expired-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(CacheEntry{
		VideoID: "expire1", Format: "mp3", Bitrate: "192",
		Path: expPath, Size: 12, CreatedAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	if c.Len() != 2 {
		t.Fatalf("len=%d want 2", c.Len())
	}

	// Advance clock past TTL=1s style expiry already set; cleanup should drop expired file.
	stats, err := c.Cleanup(0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ExpiredRemoved != 1 {
		t.Fatalf("expired removed=%d want 1 stats=%+v", stats.ExpiredRemoved, stats)
	}
	if c.Len() != 1 {
		t.Fatalf("len after cleanup=%d want 1", c.Len())
	}
	if _, err := os.Stat(expPath); !os.IsNotExist(err) {
		t.Fatalf("expired file should be deleted, err=%v", err)
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live file should remain: %v", err)
	}
	if _, ok := c.Get("liveid1", "mp3", "192"); !ok {
		t.Fatal("live entry missing")
	}
	if _, ok := c.Get("expire1", "mp3", "192"); ok {
		t.Fatal("expired entry still present")
	}
}

func TestCacheCleanupEnforcesMaxTotal(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	c, err := newCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }

	// Three files: 100, 100, 100 bytes. Max total 150 => remove oldest until <= 150.
	// Keep creation order: a (oldest), b, c (newest).
	type item struct {
		id   string
		path string
		at   time.Time
	}
	items := []item{
		{"oldaaa1", filepath.Join(dir, "a.mp3"), now.Add(-3 * time.Minute)},
		{"midbbb2", filepath.Join(dir, "b.mp3"), now.Add(-2 * time.Minute)},
		{"newccc3", filepath.Join(dir, "c.mp3"), now.Add(-1 * time.Minute)},
	}
	for _, it := range items {
		data := make([]byte, 100)
		for i := range data {
			data[i] = 'x'
		}
		if err := os.WriteFile(it.path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := c.Put(CacheEntry{
			VideoID: it.id, Format: "mp3", Bitrate: "192",
			Path: it.path, Size: 100,
			CreatedAt: it.at, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if c.TotalBytes() != 300 {
		t.Fatalf("total=%d want 300", c.TotalBytes())
	}

	stats, err := c.Cleanup(150)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SizeRemoved < 2 {
		// 300 -> need to free at least 150, so remove 2 oldest (100 each) => remain 100.
		t.Fatalf("size removed=%d want >=2 stats=%+v", stats.SizeRemoved, stats)
	}
	if c.Len() != 1 {
		t.Fatalf("len=%d want 1", c.Len())
	}
	if c.TotalBytes() > 150 {
		t.Fatalf("total=%d want <=150", c.TotalBytes())
	}
	// Oldest two gone, newest remains.
	if _, err := os.Stat(items[0].path); !os.IsNotExist(err) {
		t.Fatalf("oldest should be deleted")
	}
	if _, err := os.Stat(items[1].path); !os.IsNotExist(err) {
		t.Fatalf("second oldest should be deleted")
	}
	if _, err := os.Stat(items[2].path); err != nil {
		t.Fatalf("newest should remain: %v", err)
	}
	if _, ok := c.Get("newccc3", "mp3", "192"); !ok {
		t.Fatal("newest entry missing from index")
	}
}

func TestDownloaderCleanupUsesConfigMax(t *testing.T) {
	cfg := testConfig(t)
	cfg.CacheMaxTotalMB = 16 // normalize floor is 16MB in config, but here we set bytes via field
	// Bypass normalize: set CacheMaxTotalMB so CacheMaxTotalBytes is small enough for test.
	// Config.CacheMaxTotalBytes uses MB; for unit test call cache.Cleanup directly above.
	// Here we just ensure Downloader.Cleanup wires through without error on empty cache.
	fakeBin := filepath.Join(cfg.DownloadDir, "fake-ytdlp.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.YtdlpPath = fakeBin
	d, err := New(cfg, Options{Runner: &fakeRunner{writeSize: 64}})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := d.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 0 {
		t.Fatalf("empty cache entries=%d", stats.Entries)
	}
}
