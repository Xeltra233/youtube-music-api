package download

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// CacheEntry 是磁盘缓存索引中的一条记录。
type CacheEntry struct {
	VideoID     string    `json:"video_id"`
	Format      string    `json:"format"`
	Bitrate     string    `json:"bitrate"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	Token       string    `json:"token"`
	Title       string    `json:"title,omitempty"`
	Artists     []string  `json:"artists,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	DurationSec int       `json:"duration_seconds,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type cacheIndex struct {
	Entries map[string]CacheEntry `json:"entries"`
}

// Cache 负责缓存索引落盘与 token 映射。
//
// 文件本体由 Downloader 写到 DownloadDir；这里只管索引与查找。
// 后台 goroutine 可周期调用 Cleanup：TTL 过期 + 目录总量上限（删最旧）。
type Cache struct {
	dir       string
	ttl       time.Duration
	indexPath string
	mu        sync.Mutex
	entries   map[string]CacheEntry
	tokens    map[string]string // token -> cacheKey
	now       func() time.Time
}

func newCache(dir string, ttl time.Duration) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("download: create cache dir: %w", err)
	}
	c := &Cache{
		dir:       dir,
		ttl:       ttl,
		indexPath: filepath.Join(dir, "cache_index.json"),
		entries:   make(map[string]CacheEntry),
		tokens:    make(map[string]string),
		now:       time.Now,
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

func cacheKey(videoID, format, bitrate string) string {
	return videoID + "|" + format + "|" + bitrate
}

func (c *Cache) load() error {
	data, err := os.ReadFile(c.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("download: read cache index: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var idx cacheIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		// 损坏索引：备份后从空开始，避免服务起不来。
		_ = os.Rename(c.indexPath, c.indexPath+".corrupt")
		return nil
	}
	now := c.now()
	for k, e := range idx.Entries {
		if e.ExpiresAt.Before(now) {
			continue
		}
		if e.Path == "" || e.Token == "" {
			continue
		}
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		c.entries[k] = e
		c.tokens[e.Token] = k
	}
	return nil
}

func (c *Cache) saveLocked() error {
	idx := cacheIndex{Entries: make(map[string]CacheEntry, len(c.entries))}
	for k, e := range c.entries {
		idx.Entries[k] = e
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.indexPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.indexPath)
}

// Get 命中未过期且文件仍在的缓存。
func (c *Cache) Get(videoID, format, bitrate string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(videoID, format, bitrate)
	e, ok := c.entries[key]
	if !ok {
		return CacheEntry{}, false
	}
	if !c.now().Before(e.ExpiresAt) {
		delete(c.entries, key)
		delete(c.tokens, e.Token)
		_ = c.saveLocked()
		return CacheEntry{}, false
	}
	if st, err := os.Stat(e.Path); err != nil || st.Size() <= 0 {
		delete(c.entries, key)
		delete(c.tokens, e.Token)
		_ = c.saveLocked()
		return CacheEntry{}, false
	}
	return e, true
}

// Put 写入/覆盖缓存记录。
func (c *Cache) Put(e CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(e.VideoID, e.Format, e.Bitrate)
	if old, ok := c.entries[key]; ok {
		delete(c.tokens, old.Token)
	}
	if e.Token == "" {
		tok, err := newToken()
		if err != nil {
			return err
		}
		e.Token = tok
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = c.now()
	}
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = e.CreatedAt.Add(c.ttl)
	}
	c.entries[key] = e
	c.tokens[e.Token] = key
	return c.saveLocked()
}

// GetByToken 按 token 取缓存。
func (c *Cache) GetByToken(token string) (CacheEntry, error) {
	if err := ValidateToken(token); err != nil {
		return CacheEntry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key, ok := c.tokens[token]
	if !ok {
		return CacheEntry{}, &NotFoundError{Reason: "token not found"}
	}
	e, ok := c.entries[key]
	if !ok {
		return CacheEntry{}, &NotFoundError{Reason: "token not found"}
	}
	if !c.now().Before(e.ExpiresAt) {
		delete(c.entries, key)
		delete(c.tokens, token)
		_ = c.saveLocked()
		return CacheEntry{}, &NotFoundError{Reason: "token expired"}
	}
	if st, err := os.Stat(e.Path); err != nil || st.Size() <= 0 {
		delete(c.entries, key)
		delete(c.tokens, token)
		_ = c.saveLocked()
		return CacheEntry{}, &NotFoundError{Reason: "cached file missing"}
	}
	return e, nil
}

// RemainingTTL 返回 entry 剩余 TTL。
func (c *Cache) RemainingTTL(e CacheEntry) time.Duration {
	now := c.now()
	if !now.Before(e.ExpiresAt) {
		return 0
	}
	return e.ExpiresAt.Sub(now)
}

// CleanupStats summarizes one cleanup pass.
type CleanupStats struct {
	ExpiredRemoved int
	SizeRemoved    int
	BytesFreed     int64
	TotalBytes     int64
	Entries        int
}

// Cleanup removes expired entries (and their files), then enforces maxBytes.
// maxBytes <= 0 skips the size limit pass.
func (c *Cache) Cleanup(maxBytes int64) (CleanupStats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var stats CleanupStats
	now := c.now()
	changed := false

	for key, e := range c.entries {
		expired := !now.Before(e.ExpiresAt)
		missing := false
		if !expired {
			if st, err := os.Stat(e.Path); err != nil || st.Size() <= 0 {
				missing = true
			}
		}
		if expired || missing {
			c.removeEntryLocked(key, e, true)
			stats.ExpiredRemoved++
			changed = true
		}
	}

	// Recompute total size from remaining entries.
	type sized struct {
		key  string
		e    CacheEntry
		size int64
	}
	items := make([]sized, 0, len(c.entries))
	var total int64
	for key, e := range c.entries {
		size := e.Size
		if st, err := os.Stat(e.Path); err == nil {
			size = st.Size()
			if size != e.Size {
				e.Size = size
				c.entries[key] = e
				changed = true
			}
		}
		items = append(items, sized{key: key, e: e, size: size})
		total += size
	}

	if maxBytes > 0 && total > maxBytes {
		// Oldest first: CreatedAt, then ExpiresAt, then key for stability.
		sort.SliceStable(items, func(i, j int) bool {
			if !items[i].e.CreatedAt.Equal(items[j].e.CreatedAt) {
				return items[i].e.CreatedAt.Before(items[j].e.CreatedAt)
			}
			if !items[i].e.ExpiresAt.Equal(items[j].e.ExpiresAt) {
				return items[i].e.ExpiresAt.Before(items[j].e.ExpiresAt)
			}
			return items[i].key < items[j].key
		})
		for _, it := range items {
			if total <= maxBytes {
				break
			}
			if _, ok := c.entries[it.key]; !ok {
				continue
			}
			c.removeEntryLocked(it.key, it.e, true)
			total -= it.size
			if total < 0 {
				total = 0
			}
			stats.SizeRemoved++
			stats.BytesFreed += it.size
			changed = true
		}
	}

	stats.TotalBytes = total
	stats.Entries = len(c.entries)
	if changed {
		if err := c.saveLocked(); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// removeEntryLocked drops an index entry and optionally deletes its file.
// Caller must hold c.mu. Does not save the index.
func (c *Cache) removeEntryLocked(key string, e CacheEntry, deleteFile bool) {
	delete(c.entries, key)
	if e.Token != "" {
		delete(c.tokens, e.Token)
	}
	if !deleteFile || e.Path == "" {
		return
	}
	// Never delete the index file itself.
	if filepath.Clean(e.Path) == filepath.Clean(c.indexPath) {
		return
	}
	// Only delete files under the cache directory.
	if err := ensureUnderDir(c.dir, e.Path); err != nil {
		return
	}
	_ = os.Remove(e.Path)
}

// Len returns the number of live cache entries (for tests/metrics).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// TotalBytes returns the sum of entry sizes (uses Stat when available).
func (c *Cache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for _, e := range c.entries {
		if st, err := os.Stat(e.Path); err == nil {
			total += st.Size()
			continue
		}
		total += e.Size
	}
	return total
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极端情况下退到 sha1 时间戳，仍保持 hex 长度。
		sum := sha1.Sum([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return hex.EncodeToString(sum[:16]), nil
	}
	return hex.EncodeToString(b[:]), nil
}
