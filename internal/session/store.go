package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/search"
)

// ????????????????????????? 2 ??????????
const defaultShards = 32

// Store ??? + TTL ????????
//
// ???????????????Put/Get/Delete/Cleanup ????
// ?????Get ????? + Cleanup ??????
type Store struct {
	shards []*shard
	ttl    time.Duration
	now    func() time.Time
	// idSeq ?? fallback????????????
	idSeq atomic.Uint64
}

type shard struct {
	mu   sync.RWMutex
	data map[string]*entry
}

type entry struct {
	snapshot Snapshot
}

// Options ?? Store ????????
type Options struct {
	// TTL ?? 30 ???? config.SessionTTL ??????
	TTL time.Duration
	// ShardCount ?? 32????????? 2 ????? 1??
	ShardCount int
	// Now ????????????? time.Now?
	Now func() time.Time
}

// NewStore ???????ttl ????? 1 ??????????????
func NewStore(opts Options) *Store {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if ttl < time.Second {
		ttl = time.Second
	}

	n := opts.ShardCount
	if n <= 0 {
		n = defaultShards
	}
	// ??? 2 ???
	n = nextPowerOfTwo(n)

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	shards := make([]*shard, n)
	for i := range shards {
		shards[i] = &shard{data: make(map[string]*entry)}
	}
	return &Store{shards: shards, ttl: ttl, now: now}
}

// TTL ????????????
func (s *Store) TTL() time.Duration {
	return s.ttl
}

// Put ?????????? session_id ? expires_in????
//
// results ??????? Artists ??????????????? store?
// query ????/???????
func (s *Store) Put(query string, results []search.Item) (sessionID string, expiresIn int, err error) {
	if s == nil {
		return "", 0, fmt.Errorf("session: nil store")
	}

	id, err := s.newID()
	if err != nil {
		return "", 0, err
	}

	now := s.now()
	exp := now.Add(s.ttl)
	snap := Snapshot{
		ID:        id,
		Query:     query,
		Results:   cloneItems(results),
		CreatedAt: now,
		ExpiresAt: exp,
	}

	sh := s.shardFor(id)
	sh.mu.Lock()
	sh.data[id] = &entry{snapshot: snap}
	sh.mu.Unlock()

	return id, snap.ExpiresInSeconds(now), nil
}

// Get ???????????
//
// session ??? ? ErrNotFound???? ? ErrGone????????
func (s *Store) Get(sessionID string) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, fmt.Errorf("session: nil store")
	}
	if sessionID == "" {
		return Snapshot{}, &NotFoundError{Reason: "empty session_id"}
	}

	sh := s.shardFor(sessionID)
	now := s.now()

	sh.mu.RLock()
	ent, ok := sh.data[sessionID]
	if !ok {
		sh.mu.RUnlock()
		return Snapshot{}, &NotFoundError{Reason: "session not found"}
	}
	// ???????????????????????
	snap := ent.snapshot
	expired := !now.Before(snap.ExpiresAt)
	sh.mu.RUnlock()

	if expired {
		// ??????? Cleanup/Get ????????
		s.Delete(sessionID)
		return Snapshot{}, &GoneError{SessionID: sessionID}
	}

	// ???????????? Results/Artists ?? store?
	out := snap
	out.Results = cloneItems(snap.Results)
	return out, nil
}

// Delete ????????????????
func (s *Store) Delete(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	sh := s.shardFor(sessionID)
	sh.mu.Lock()
	delete(sh.data, sessionID)
	sh.mu.Unlock()
}

// Cleanup ??????????????????????
// ????? goroutine ? CleanupInterval ???
func (s *Store) Cleanup() int {
	if s == nil {
		return 0
	}
	now := s.now()
	removed := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		for id, ent := range sh.data {
			if !now.Before(ent.snapshot.ExpiresAt) {
				delete(sh.data, id)
				removed++
			}
		}
		sh.mu.Unlock()
	}
	return removed
}

// Len ???????????????????????????
// ??????????
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		n += len(sh.data)
		sh.mu.RUnlock()
	}
	return n
}

func (s *Store) shardFor(id string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	// shards ??? 2 ??????????
	return s.shards[h.Sum32()&uint32(len(s.shards)-1)]
}

func (s *Store) newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// ????????????????????????????
		n := s.idSeq.Add(1)
		return fmt.Sprintf("s_%016x", n), nil
	}
	return "s_" + hex.EncodeToString(b[:]), nil
}

func cloneItems(in []search.Item) []search.Item {
	if in == nil {
		return []search.Item{}
	}
	out := make([]search.Item, len(in))
	for i, it := range in {
		out[i] = it
		if it.Artists == nil {
			out[i].Artists = []string{}
		} else {
			out[i].Artists = append([]string(nil), it.Artists...)
		}
	}
	return out
}

func nextPowerOfTwo(n int) int {
	if n < 1 {
		return 1
	}
	// ??? 2 ???
	if n&(n-1) == 0 {
		return n
	}
	p := 1
	for p < n {
		p <<= 1
		if p <= 0 {
			// ????? defaultShards?
			return defaultShards
		}
	}
	return p
}
