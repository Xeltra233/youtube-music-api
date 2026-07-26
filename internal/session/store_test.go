package session

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/search"
)

// fakeClock ?????????
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func sampleItems(n int) []search.Item {
	out := make([]search.Item, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, search.Item{
			Index:           i,
			DisplayName:     fmt.Sprintf("Song %02d - Artist %02d", i, i),
			Title:           fmt.Sprintf("Song %02d", i),
			Artists:         []string{fmt.Sprintf("Artist %02d", i)},
			Album:           "Album",
			Duration:        "3:00",
			DurationSeconds: 180,
			VideoID:         fmt.Sprintf("vid%02d", i),
			Thumbnail:       "http://img",
			MatchScore:      1.0 - float64(i)*0.01,
		})
	}
	return out
}

func TestPutGetRoundTrip(t *testing.T) {
	store := NewStore(Options{TTL: 5 * time.Minute, ShardCount: 4})
	items := sampleItems(3)
	id, exp, err := store.Put("query", items)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || !hasSessionPrefix(id) {
		t.Fatalf("id=%q", id)
	}
	if exp != int((5 * time.Minute).Seconds()) {
		t.Fatalf("expires_in=%d want 300", exp)
	}

	snap, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != id || snap.Query != "query" {
		t.Fatalf("snap meta: %+v", snap)
	}
	if len(snap.Results) != 3 {
		t.Fatalf("results=%d", len(snap.Results))
	}
	if snap.Results[0].Index != 1 || snap.Results[2].Index != 3 {
		t.Fatalf("index mapping broken: %+v", snap.Results)
	}
	// ??????????? store?
	snap.Results[0].Title = "MUTATED"
	snap.Results[0].Artists[0] = "MUTATED"
	again, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Results[0].Title == "MUTATED" || again.Results[0].Artists[0] == "MUTATED" {
		t.Fatal("store was mutated via returned snapshot")
	}
}

func hasSessionPrefix(id string) bool {
	return len(id) > 2 && id[:2] == "s_"
}

func TestGetUnknownSession(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	_, err := store.Get("s_does_not_exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestGetExpiredReturnsGone(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC))
	store := NewStore(Options{
		TTL:        30 * time.Second,
		ShardCount: 2,
		Now:        clock.Now,
	})
	id, _, err := store.Put("q", sampleItems(1))
	if err != nil {
		t.Fatal(err)
	}
	// ???????ExpiresAt == now ?????!now.Before??
	clock.Advance(30 * time.Second)
	_, err = store.Get(id)
	if !errors.Is(err, ErrGone) {
		t.Fatalf("err=%v want ErrGone", err)
	}
	var ge *GoneError
	if !errors.As(err, &ge) || ge.SessionID != id {
		t.Fatalf("GoneError detail: %v", err)
	}
	// ????? Len ?? 0?
	if store.Len() != 0 {
		t.Fatalf("len after lazy delete=%d", store.Len())
	}
}

func TestCleanupRemovesExpired(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := NewStore(Options{TTL: time.Minute, Now: clock.Now, ShardCount: 8})
	for i := 0; i < 5; i++ {
		if _, _, err := store.Put("q", sampleItems(1)); err != nil {
			t.Fatal(err)
		}
	}
	if store.Len() != 5 {
		t.Fatalf("len=%d", store.Len())
	}
	clock.Advance(61 * time.Second)
	removed := store.Cleanup()
	if removed != 5 {
		t.Fatalf("removed=%d want 5", removed)
	}
	if store.Len() != 0 {
		t.Fatalf("len after cleanup=%d", store.Len())
	}
}

func TestPutDefensiveCopy(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	items := sampleItems(1)
	id, _, err := store.Put("q", items)
	if err != nil {
		t.Fatal(err)
	}
	items[0].Title = "changed-after-put"
	items[0].Artists[0] = "changed-after-put"
	snap, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Results[0].Title == "changed-after-put" {
		t.Fatal("put did not clone items")
	}
}

func TestDelete(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id, _, _ := store.Put("q", sampleItems(1))
	store.Delete(id)
	_, err := store.Get(id)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrentPutGetNoRace(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute, ShardCount: 16})
	const writers = 32
	const readers = 32
	const perWriter = 50

	ids := make(chan string, writers*perWriter)
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id, _, err := store.Put(fmt.Sprintf("q-%d-%d", w, i), sampleItems(3))
				if err != nil {
					t.Errorf("put: %v", err)
					return
				}
				ids <- id
			}
		}(w)
	}
	wg.Wait()
	close(ids)

	var got atomic.Int64
	wg.Add(readers)
	// ? id ??????????
	all := make([]string, 0, writers*perWriter)
	for id := range ids {
		all = append(all, id)
	}
	for r := 0; r < readers; r++ {
		go func(r int) {
			defer wg.Done()
			for i := 0; i < len(all); i++ {
				id := all[(i+r)%len(all)]
				snap, err := store.Get(id)
				if err != nil {
					t.Errorf("get %s: %v", id, err)
					return
				}
				if len(snap.Results) != 3 {
					t.Errorf("results=%d", len(snap.Results))
					return
				}
				got.Add(1)
			}
		}(r)
	}
	wg.Wait()
	if got.Load() == 0 {
		t.Fatal("no successful reads")
	}
}

func TestDefaultTTLAndShardCount(t *testing.T) {
	store := NewStore(Options{})
	if store.TTL() != 30*time.Minute {
		t.Fatalf("ttl=%v", store.TTL())
	}
	if len(store.shards) != defaultShards {
		t.Fatalf("shards=%d", len(store.shards))
	}
	// ? 2 ?????
	s2 := NewStore(Options{ShardCount: 3})
	if len(s2.shards) != 4 {
		t.Fatalf("shards=%d want 4", len(s2.shards))
	}
}

func BenchmarkStorePut(b *testing.B) {
	store := NewStore(Options{TTL: time.Minute, ShardCount: 32})
	items := sampleItems(10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := store.Put("q", items); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreGet(b *testing.B) {
	store := NewStore(Options{TTL: time.Hour, ShardCount: 32})
	id, _, err := store.Put("q", sampleItems(10))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Get(id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStorePutGetParallel(b *testing.B) {
	store := NewStore(Options{TTL: time.Hour, ShardCount: 32})
	items := sampleItems(10)
	// ???? key??? Get ? miss?
	ids := make([]string, 64)
	for i := range ids {
		id, _, err := store.Put("q", items)
		if err != nil {
			b.Fatal(err)
		}
		ids[i] = id
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				if _, _, err := store.Put("q", items); err != nil {
					b.Error(err)
					return
				}
			} else {
				if _, err := store.Get(ids[i%len(ids)]); err != nil {
					b.Error(err)
					return
				}
			}
			i++
		}
	})
}

func TestExpiresInSecondsSubSecond(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{ExpiresAt: now.Add(500 * time.Millisecond)}
	if got := snap.ExpiresInSeconds(now); got != 1 {
		t.Fatalf("got %d want 1", got)
	}
	if got := snap.ExpiresInSeconds(now.Add(500 * time.Millisecond)); got != 0 {
		t.Fatalf("boundary got %d", got)
	}
}

func TestPutNilResults(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id, _, err := store.Put("q", nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Results == nil || len(snap.Results) != 0 {
		t.Fatalf("%+v", snap.Results)
	}
}
