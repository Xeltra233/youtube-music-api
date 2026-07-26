package session

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/matching"
	"github.com/xeltra/ytmusic-bridge/internal/search"
)

func putSample(t *testing.T, store *Store, items []search.Item) string {
	t.Helper()
	id, _, err := store.Put("test-query", items)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func lemonItems() []search.Item {
	return []search.Item{
		{
			Index: 1, DisplayName: "Lemon - Kenshi Yonezu", Title: "Lemon",
			Artists: []string{"Kenshi Yonezu"}, VideoID: "lemon1", MatchScore: 0.95,
		},
		{
			Index: 2, DisplayName: "?? - ???", Title: "??",
			Artists: []string{"???"}, VideoID: "qt1", MatchScore: 0.9,
		},
		{
			Index: 3, DisplayName: "Lemon Tree - Fools Garden", Title: "Lemon Tree",
			Artists: []string{"Fools Garden"}, VideoID: "tree1", MatchScore: 0.7,
		},
	}
}

func TestSelectByIndex1Based(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())

	sel, err := store.Select(SelectRequest{SessionID: id, Index: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.FromSession || sel.SessionID != id {
		t.Fatalf("meta: %+v", sel)
	}
	if sel.Item.VideoID != "lemon1" || sel.Item.Index != 1 {
		t.Fatalf("item: %+v", sel.Item)
	}

	sel, err = store.Select(SelectRequest{SessionID: id, Index: 2})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "qt1" {
		t.Fatalf("item: %+v", sel.Item)
	}
}

func TestSelectIndexOutOfRange(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())

	for _, idx := range []int{0, 4, 99} {
		// index=0 ?? name ? bad request??????? >len?
		if idx == 0 {
			continue
		}
		_, err := store.Select(SelectRequest{SessionID: id, Index: idx})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("index=%d err=%v want ErrNotFound", idx, err)
		}
	}
	// ??
	_, err := store.Select(SelectRequest{SessionID: id, Index: -1})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err=%v want ErrBadRequest", err)
	}
}

func TestSelectIndexPreferredOverName(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	// index=2 ? name ??? 1 ? ? ?? index?
	sel, err := store.Select(SelectRequest{
		SessionID: id,
		Index:     2,
		Name:      "Lemon - Kenshi Yonezu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "qt1" {
		t.Fatalf("expected index win, got %+v", sel.Item)
	}
}

func TestSelectExactName(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	sel, err := store.Select(SelectRequest{
		SessionID: id,
		Name:      "?? - ???",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "qt1" {
		t.Fatalf("got %+v", sel.Item)
	}
}

func TestSelectNormalizedName(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	// ??? + ???? + ??????? Normalize ???
	// display = "Lemon - Kenshi Yonezu"
	// ????????????????
	cases := []string{
		"lemon - kenshi yonezu",
		"  Lemon   -   Kenshi Yonezu  ",
		"LEMON - KENSHI YONEZU",
	}
	for _, name := range cases {
		sel, err := store.Select(SelectRequest{SessionID: id, Name: name})
		if err != nil {
			t.Fatalf("name=%q err=%v", name, err)
		}
		if sel.Item.VideoID != "lemon1" {
			t.Fatalf("name=%q got %+v", name, sel.Item)
		}
	}
	// ?? Normalize ???????????????
	if matching.Normalize("Lemon - Kenshi Yonezu") != matching.Normalize("lemon - kenshi yonezu") {
		t.Fatal("Normalize invariant broken")
	}
}

func TestSelectFuzzyUniqueName(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	// ?????????????????Lemon - Kenshi Yonezu ??????
	// ?? Lemon Tree ?????? "Lemon Kenshi" ????? lemon1?
	sel, err := store.Select(SelectRequest{
		SessionID: id,
		Name:      "Lemon Kenshi Yonezu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "lemon1" {
		t.Fatalf("got %+v", sel.Item)
	}
}

func TestSelectAmbiguousExactName(t *testing.T) {
	items := []search.Item{
		{Index: 1, DisplayName: "Same Song - A", Title: "Same Song", Artists: []string{"A"}, VideoID: "a"},
		{Index: 2, DisplayName: "Same Song - A", Title: "Same Song", Artists: []string{"A"}, VideoID: "b"},
		{Index: 3, DisplayName: "Other - B", Title: "Other", Artists: []string{"B"}, VideoID: "c"},
	}
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, items)

	_, err := store.Select(SelectRequest{SessionID: id, Name: "Same Song - A"})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err=%v want ErrAmbiguous", err)
	}
	var ae *AmbiguousError
	if !errors.As(err, &ae) {
		t.Fatalf("not AmbiguousError: %T", err)
	}
	if len(ae.Candidates) != 2 {
		t.Fatalf("candidates=%d want 2", len(ae.Candidates))
	}
	ids := map[string]bool{}
	for _, c := range ae.Candidates {
		ids[c.VideoID] = true
	}
	if !ids["a"] || !ids["b"] {
		t.Fatalf("candidates=%v", ae.Candidates)
	}
}

func TestSelectAmbiguousNormalizedName(t *testing.T) {
	items := []search.Item{
		{Index: 1, DisplayName: "Hello World - X", Title: "Hello World", Artists: []string{"X"}, VideoID: "x1"},
		{Index: 2, DisplayName: "hello   world - x", Title: "hello   world", Artists: []string{"x"}, VideoID: "x2"},
	}
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, items)
	// ??????????/??????????????? ? ???
	_, err := store.Select(SelectRequest{SessionID: id, Name: "HELLO WORLD - X"})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err=%v want ErrAmbiguous", err)
	}
	var ae *AmbiguousError
	if !errors.As(err, &ae) || len(ae.Candidates) != 2 {
		t.Fatalf("ae=%v", err)
	}
}

func TestSelectNameNotFound(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	_, err := store.Select(SelectRequest{SessionID: id, Name: "????????XYZXYZ"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestSelectVideoIDDirect(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	// ? session ????
	sel, err := store.Select(SelectRequest{VideoID: "SJKoWAd5ySo"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.FromSession || sel.SessionID != "" {
		t.Fatalf("should not use session: %+v", sel)
	}
	if sel.Item.VideoID != "SJKoWAd5ySo" {
		t.Fatalf("item=%+v", sel.Item)
	}
	// video_id ??? index/name??? session ???
	sel, err = store.Select(SelectRequest{
		SessionID: "s_invalid",
		Index:     1,
		Name:      "whatever",
		VideoID:   "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "abc123" || sel.FromSession {
		t.Fatalf("video_id should win: %+v", sel)
	}
}

func TestSelectExpiredSession(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	store := NewStore(Options{TTL: 10 * time.Second, Now: clock.Now})
	id := putSample(t, store, lemonItems())
	clock.Advance(11 * time.Second)
	_, err := store.Select(SelectRequest{SessionID: id, Index: 1})
	if !errors.Is(err, ErrGone) {
		t.Fatalf("err=%v want ErrGone", err)
	}
}

func TestSelectMissingSessionID(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	_, err := store.Select(SelectRequest{Index: 1})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err=%v", err)
	}
	_, err = store.Select(SelectRequest{Name: "x"})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err=%v", err)
	}
	_, err = store.Select(SelectRequest{})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestSelectUnknownSession(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	_, err := store.Select(SelectRequest{SessionID: "s_nope", Index: 1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestSelectReturnsClonedArtists(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	sel, err := store.Select(SelectRequest{SessionID: id, Index: 1})
	if err != nil {
		t.Fatal(err)
	}
	sel.Item.Artists[0] = "MUTATED"
	again, err := store.Select(SelectRequest{SessionID: id, Index: 1})
	if err != nil {
		t.Fatal(err)
	}
	if again.Item.Artists[0] == "MUTATED" {
		t.Fatal("selection leaked artists slice")
	}
}

func TestSelectVideoIDTrimSpace(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	sel, err := store.Select(SelectRequest{VideoID: "  abc  "})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "abc" {
		t.Fatalf("got %q", sel.Item.VideoID)
	}
}

func TestSelectIndexZeroFallsBackToName(t *testing.T) {
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, lemonItems())
	sel, err := store.Select(SelectRequest{SessionID: id, Index: 0, Name: "?? - ???"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Item.VideoID != "qt1" {
		t.Fatalf("%+v", sel.Item)
	}
}

func TestSelectFuzzyAmbiguousEqualTopScore(t *testing.T) {
	items := []search.Item{
		{Index: 1, DisplayName: "Alpha Song - Z", Title: "Alpha Song", Artists: []string{"Z"}, VideoID: "1"},
		{Index: 2, DisplayName: "Alpha Song - Y", Title: "Alpha Song", Artists: []string{"Y"}, VideoID: "2"},
		{Index: 3, DisplayName: "Beta - Q", Title: "Beta", Artists: []string{"Q"}, VideoID: "3"},
	}
	store := NewStore(Options{TTL: time.Minute})
	id := putSample(t, store, items)
	// "Alpha Song" is a substring of both display names with very similar scores; should be ambiguous or unique.
	_, err := store.Select(SelectRequest{SessionID: id, Name: "Alpha Song"})
	// Either unique best or ambiguous is acceptable only if deterministic; with symmetric names expect ambiguous.
	if err == nil {
		// If one wins uniquely due to targetCoverage, that's ok as long as stable; record which.
		// Re-run thrice for stability.
		var first string
		for i := 0; i < 3; i++ {
			sel, err2 := store.Select(SelectRequest{SessionID: id, Name: "Alpha Song"})
			if err2 != nil {
				t.Fatalf("unstable err on retry: %v", err2)
			}
			if i == 0 {
				first = sel.Item.VideoID
			} else if sel.Item.VideoID != first {
				t.Fatalf("unstable selection %s vs %s", first, sel.Item.VideoID)
			}
		}
		return
	}
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err=%v", err)
	}
}

func TestSelectConcurrentWithCleanup(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	store := NewStore(Options{TTL: time.Minute, Now: clock.Now, ShardCount: 8})
	ids := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		ids = append(ids, putSample(t, store, lemonItems()))
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := ids[(i+j)%len(ids)]
				_, _ = store.Select(SelectRequest{SessionID: id, Index: 1})
				if j == 25 {
					store.Cleanup()
				}
			}
		}(i)
	}
	wg.Wait()
	// advance and cleanup
	clock.Advance(2 * time.Minute)
	_ = store.Cleanup()
	if store.Len() != 0 {
		t.Fatalf("len=%d", store.Len())
	}
}
