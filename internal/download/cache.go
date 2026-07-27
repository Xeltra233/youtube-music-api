package download

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
// 后台 TTL / 总量清理留给 G8，本层只做惰性过期判断。
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

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极端情况下退到 sha1 时间戳，仍保持 hex 长度。
		sum := sha1.Sum([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return hex.EncodeToString(sum[:16]), nil
	}
	return hex.EncodeToString(b[:]), nil
}
