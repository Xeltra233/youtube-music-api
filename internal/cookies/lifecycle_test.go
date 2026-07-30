package cookies

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleBrowserFake struct {
	mu     sync.Mutex
	calls  []BrowserSyncOptions
	callCh chan int
	run    func(context.Context, BrowserSyncOptions, int) (BrowserSyncResult, error)
}

func (f *lifecycleBrowserFake) Sync(ctx context.Context, opt BrowserSyncOptions) (BrowserSyncResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opt)
	call := len(f.calls)
	run := f.run
	f.mu.Unlock()
	if f.callCh != nil {
		f.callCh <- call
	}
	if run != nil {
		return run(ctx, opt, call)
	}
	return BrowserSyncResult{}, nil
}

func (f *lifecycleBrowserFake) snapshotCalls() []BrowserSyncOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]BrowserSyncOptions(nil), f.calls...)
}

type lifecycleLogRecorder struct {
	mu      sync.Mutex
	entries []string
	entryCh chan string
}

func newLifecycleLogRecorder() *lifecycleLogRecorder {
	return &lifecycleLogRecorder{entryCh: make(chan string, 32)}
}

func (r *lifecycleLogRecorder) Logf(format string, args ...any) {
	entry := fmt.Sprintf(format, args...)
	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()
	select {
	case r.entryCh <- entry:
	default:
	}
}

func (r *lifecycleLogRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.entries, "\n")
}

func (r *lifecycleLogRecorder) waitContains(t *testing.T, want string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(r.String(), want) {
			return
		}
		select {
		case <-r.entryCh:
		case <-deadline.C:
			t.Fatalf("log never contained %q; got %q", want, r.String())
		}
	}
}

func TestCookieLifecycleStartupForwardsOptionsBeforeReturning(t *testing.T) {
	spec := `chrome:C:\Users\Example User\Profile 1`
	proxy := "http://user:proxy-token@127.0.0.1:7890"
	fake := &lifecycleBrowserFake{
		run: func(_ context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			return BrowserSyncResult{
				Updated:        true,
				LoggedIn:       true,
				CandidateScore: 30,
				StableScore:    30,
				CookieCount:    9,
			}, nil
		},
	}
	logs := newLifecycleLogRecorder()
	lifecycle := NewCookieLifecycle(fake, nil, logs.Logf)
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		StableFile:         `C:\cookie data\youtube.txt`,
		YtdlpPath:          `C:\tools\yt-dlp.exe`,
		Proxy:              proxy,
		BrowserSpec:        spec,
		BrowserSyncOnStart: true,
		BrowserSyncTimeout: 17 * time.Second,
	})

	calls := fake.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("startup calls=%d want 1", len(calls))
	}
	got := calls[0]
	if got.BrowserSpec != spec || got.StableFile != `C:\cookie data\youtube.txt` ||
		got.YtdlpPath != `C:\tools\yt-dlp.exe` || got.Proxy != proxy || got.Timeout != 17*time.Second {
		t.Fatalf("forwarded options mismatch: %+v", got)
	}
	logText := logs.String()
	if !strings.Contains(logText, "startup updated=true logged_in=true cookies=9") {
		t.Fatalf("missing metadata result log: %q", logText)
	}
	for _, secret := range []string{spec, proxy, `C:\cookie data`, `C:\tools`} {
		if strings.Contains(logText, secret) {
			t.Fatalf("lifecycle log leaked option %q: %q", secret, logText)
		}
	}
}

func TestCookieLifecycleStartupSkipsDisabledOrMissingSource(t *testing.T) {
	fake := &lifecycleBrowserFake{}
	lifecycle := NewCookieLifecycle(fake, nil, nil)
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		BrowserSpec:        "chrome:Default",
		BrowserSyncOnStart: false,
	})
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		BrowserSyncOnStart: true,
	})
	if calls := fake.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("disabled startup unexpectedly synchronized: %d", len(calls))
	}
}

func TestCookieLifecycleStartupFailurePreservesExistingJarAndReturns(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	before := []byte(sampleNetscape(true) + "# existing-stable\n")
	if err := os.WriteFile(stable, before, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleBrowserFake{
		run: func(_ context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			return BrowserSyncResult{}, errors.New("cookies: browser profile database is locked")
		},
	}
	logs := newLifecycleLogRecorder()
	lifecycle := NewCookieLifecycle(fake, nil, logs.Logf)
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		StableFile:         stable,
		BrowserSpec:        "chrome:Default",
		BrowserSyncOnStart: true,
	})
	after, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("startup failure changed existing stable jar")
	}
	if !strings.Contains(logs.String(), "startup failed") || !strings.Contains(logs.String(), "locked") {
		t.Fatalf("startup failure was not reported: %q", logs.String())
	}
}

func TestCookieLifecyclePeriodicLoopContinuesAfterFailure(t *testing.T) {
	callCh := make(chan int, 2)
	fake := &lifecycleBrowserFake{
		callCh: callCh,
		run: func(_ context.Context, _ BrowserSyncOptions, call int) (BrowserSyncResult, error) {
			if call == 1 {
				return BrowserSyncResult{}, errors.New("cookies: browser profile is unavailable")
			}
			return BrowserSyncResult{Updated: true, LoggedIn: true, CookieCount: 4}, nil
		},
	}
	logs := newLifecycleLogRecorder()
	lifecycle := NewCookieLifecycle(fake, nil, logs.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	browserTicks := make(chan time.Time, 2)
	stable := filepath.Join(t.TempDir(), StableFileName)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.runLoop(ctx, CookieLifecycleOptions{
			BrowserSpec: "chrome:Default",
			StableFile:  stable,
		}, browserTicks, nil, nil)
	}()

	browserTicks <- time.Now()
	waitInt(t, callCh, 1)
	browserTicks <- time.Now()
	waitInt(t, callCh, 2)
	logs.waitContains(t, "periodic updated=true")
	cancel()
	waitDone(t, done)
	if !strings.Contains(logs.String(), "periodic failed") {
		t.Fatalf("first periodic failure missing from log: %q", logs.String())
	}
}

type lifecycleActivity struct {
	active    atomic.Int32
	maxActive atomic.Int32
}

func (a *lifecycleActivity) enter() func() {
	active := a.active.Add(1)
	for {
		maximum := a.maxActive.Load()
		if active <= maximum || a.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	return func() { a.active.Add(-1) }
}

func TestCookieLifecycleSerializesBrowserAndKeepAliveTicks(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	activity := &lifecycleActivity{}
	order := make(chan string, 2)
	releaseBrowser := make(chan struct{})
	fake := &lifecycleBrowserFake{
		run: func(_ context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			leave := activity.enter()
			defer leave()
			order <- "browser"
			<-releaseBrowser
			return BrowserSyncResult{LoggedIn: true}, nil
		},
	}
	var gotKeepAliveURLs []string
	keepAlive := func(_ context.Context, opt KeepAliveOptions) error {
		leave := activity.enter()
		defer leave()
		order <- "keepalive"
		gotKeepAliveURLs = append([]string(nil), opt.URLs...)
		f, err := os.OpenFile(opt.CookiesFile, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(".youtube.com\tTRUE\t/\tFALSE\t0\tLIFECYCLE_REFRESH\tupdated\n")
		closeErr := f.Close()
		return errors.Join(writeErr, closeErr)
	}
	logs := newLifecycleLogRecorder()
	lifecycle := NewCookieLifecycle(fake, keepAlive, logs.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	browserTicks := make(chan time.Time, 1)
	keepAliveTicks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.runLoop(ctx, CookieLifecycleOptions{
			CookiesDir:    dir,
			StableFile:    stable,
			BrowserSpec:   "chrome:Default",
			KeepAliveURLs: []string{"https://example.test/one", "https://example.test/two"},
		}, browserTicks, keepAliveTicks, nil)
	}()

	browserTicks <- time.Now()
	if got := waitString(t, order); got != "browser" {
		t.Fatalf("first operation=%q", got)
	}
	keepAliveTicks <- time.Now()
	select {
	case got := <-order:
		t.Fatalf("keepalive overlapped blocked browser operation: %q", got)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseBrowser)
	if got := waitString(t, order); got != "keepalive" {
		t.Fatalf("second operation=%q", got)
	}
	logs.waitContains(t, "cookies keepalive: refreshed stable jar")
	if got := activity.maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent cookie operations=%d want 1", got)
	}
	if strings.Join(gotKeepAliveURLs, ",") != "https://example.test/one,https://example.test/two" {
		t.Fatalf("keepalive URLs=%v", gotKeepAliveURLs)
	}
	body, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "LIFECYCLE_REFRESH") {
		t.Fatal("keepalive snapshot was not committed")
	}
	cancel()
	waitDone(t, done)
	assertNoLifecycleTemps(t, dir)
}

func TestCookieLifecycleCancellationStopsInFlightBrowserSync(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	fake := &lifecycleBrowserFake{
		run: func(ctx context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return BrowserSyncResult{}, ctx.Err()
		},
	}
	lifecycle := NewCookieLifecycle(fake, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	browserTicks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.runLoop(ctx, CookieLifecycleOptions{BrowserSpec: "chrome:Default"}, browserTicks, nil, nil)
	}()
	browserTicks <- time.Now()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("browser sync did not start")
	}
	cancel()
	waitDone(t, done)
	if calls := fake.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("browser calls=%d want 1", len(calls))
	}
}

func TestCookieLifecycleCancellationStopsInFlightKeepAliveAndCleansSnapshot(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var once sync.Once
	keepAlive := func(ctx context.Context, _ KeepAliveOptions) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}
	lifecycle := NewCookieLifecycle(nil, keepAlive, nil)
	ctx, cancel := context.WithCancel(context.Background())
	keepAliveTicks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.runLoop(ctx, CookieLifecycleOptions{StableFile: stable}, nil, keepAliveTicks, nil)
	}()
	keepAliveTicks <- time.Now()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not start")
	}
	cancel()
	waitDone(t, done)
	assertNoLifecycleTemps(t, dir)
}

func TestCookieLifecycleRunSchedulesPeriodicSyncAndStops(t *testing.T) {
	callCh := make(chan int, 4)
	fake := &lifecycleBrowserFake{callCh: callCh}
	logs := newLifecycleLogRecorder()
	lifecycle := NewCookieLifecycle(fake, nil, logs.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.Run(ctx, CookieLifecycleOptions{
			BrowserSpec:      "chrome:Default",
			BrowserSyncEvery: 5 * time.Millisecond,
		})
	}()
	waitInt(t, callCh, 1)
	cancel()
	waitDone(t, done)
	if !strings.Contains(logs.String(), "enabled every 5ms") {
		t.Fatalf("periodic schedule was not logged: %q", logs.String())
	}
}

func TestCookieLifecycleRunSchedulesKeepAliveAndResetsTimer(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := make(chan int, 8)
	var count atomic.Int32
	keepAlive := func(_ context.Context, opt KeepAliveOptions) error {
		call := int(count.Add(1))
		calls <- call
		return os.WriteFile(opt.CookiesFile, []byte(sampleNetscape(true)), 0o600)
	}
	lifecycle := NewCookieLifecycle(nil, keepAlive, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.Run(ctx, CookieLifecycleOptions{
			StableFile:            stable,
			KeepAliveEnabled:      true,
			KeepAliveInitialDelay: 5 * time.Millisecond,
			KeepAliveEvery:        10 * time.Millisecond,
		})
	}()
	waitInt(t, calls, 1)
	waitInt(t, calls, 2)
	cancel()
	waitDone(t, done)
	assertNoLifecycleTemps(t, dir)
}

func TestCookieLifecycleRunReturnsImmediatelyWhenDisabled(t *testing.T) {
	lifecycle := NewCookieLifecycle(&lifecycleBrowserFake{}, nil, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.Run(context.Background(), CookieLifecycleOptions{})
	}()
	waitDone(t, done)
}

func TestCookieLifecycleLoopReturnsAfterTickChannelsClose(t *testing.T) {
	lifecycle := NewCookieLifecycle(&lifecycleBrowserFake{}, nil, nil)
	browserTicks := make(chan time.Time)
	keepAliveTicks := make(chan time.Time)
	close(browserTicks)
	close(keepAliveTicks)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.runLoop(context.Background(), CookieLifecycleOptions{}, browserTicks, keepAliveTicks, nil)
	}()
	waitDone(t, done)
}

func waitInt(t *testing.T, ch <-chan int, want int) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("got call %d want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for call %d", want)
	}
}

func waitString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle operation")
		return ""
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle goroutine did not stop")
	}
}

func assertNoLifecycleTemps(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{".browser-cookies-*.tmp", ".ytdlp-cookies-*.tmp", ".youtube.txt.tmp-*", ".youtube.txt.bak-*"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) > 0 {
			t.Fatalf("lifecycle left temporary files for %s: %v", pattern, matches)
		}
	}
}
