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

type browserRunnerFunc func(ctx context.Context, name string, args []string) (stdout, stderr string, err error)

func (f browserRunnerFunc) Run(ctx context.Context, name string, args []string) (string, string, error) {
	return f(ctx, name, args)
}

func browserArgValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

func anonymousNetscape() string {
	return "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tFALSE\t0\tVISITOR_INFO1_LIVE\tvisitor\n"
}

func TestBrowserSyncSuccessUsesSingleSpecArgAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	spec := `chrome:C:\Users\Test User\Chrome Profile\Default`
	probe := "https://www.youtube.com/watch?v=test-probe"
	var gotName string
	var gotArgs []string
	var tempPath string
	runner := browserRunnerFunc(func(_ context.Context, name string, args []string) (string, string, error) {
		gotName = name
		gotArgs = append([]string(nil), args...)
		var ok bool
		tempPath, ok = browserArgValue(args, "--cookies")
		if !ok {
			return "", "", errors.New("missing cookies argument")
		}
		if err := os.WriteFile(tempPath, []byte(sampleNetscape(true)), 0o600); err != nil {
			return "", "", err
		}
		return "", "", nil
	})

	result, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: spec,
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		ProbeURL:    probe,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || !result.LoggedIn || result.CandidateScore <= 0 || result.StableScore <= 0 || result.CookieCount == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if gotName != "yt-dlp-test" {
		t.Fatalf("runner name=%q", gotName)
	}
	gotSpec, ok := browserArgValue(gotArgs, "--cookies-from-browser")
	if !ok || gotSpec != spec {
		t.Fatalf("browser spec must remain one exact argv: got %q in %#v", gotSpec, gotArgs)
	}
	if gotURL, ok := browserArgValue(gotArgs, "--"); !ok || gotURL != probe {
		t.Fatalf("probe URL=%q args=%#v", gotURL, gotArgs)
	}
	body, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "LOGIN_INFO") {
		t.Fatal("stable jar did not receive authenticated candidate")
	}
	if tempPath == "" {
		t.Fatal("runner did not observe temporary jar")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temporary jar still exists: %v", err)
	}
	assertNoBrowserTemps(t, dir)
}

func TestBrowserSyncPassesProxyAsSeparateArguments(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	proxy := "http://user:proxy-secret@127.0.0.1:7890"
	var gotArgs []string
	runner := browserRunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		gotArgs = append([]string(nil), args...)
		path, ok := browserArgValue(args, "--cookies")
		if !ok {
			return "", "", errors.New("missing cookies argument")
		}
		return "", "", os.WriteFile(path, []byte(sampleNetscape(true)), 0o600)
	})

	if _, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: "firefox:Profile With Spaces",
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Proxy:       proxy,
		Timeout:     time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if gotProxy, ok := browserArgValue(gotArgs, "--proxy"); !ok || gotProxy != proxy {
		t.Fatalf("proxy must be a separate exact argv: got %q in %#v", gotProxy, gotArgs)
	}
}

func TestBrowserSyncRunnerFailurePreservesStableAndRedactsDetails(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	before := []byte(sampleNetscape(true) + "# stable-marker\n")
	if err := os.WriteFile(stable, before, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := `chrome:C:\Users\Sensitive User\Secret Profile`
	var tempPath string
	runner := browserRunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		tempPath, _ = browserArgValue(args, "--cookies")
		return "LOGIN_INFO=secret-cookie " + spec,
			"database is locked at " + tempPath,
			errors.New("exit status 1")
	})

	_, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: spec,
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Proxy:       "http://user:proxy-secret@127.0.0.1:7890",
		Timeout:     time.Second,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("expected classified database lock error, got %v", err)
	}
	for _, secret := range []string{spec, tempPath, "secret-cookie", "proxy-secret"} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked sensitive detail %q: %v", secret, err)
		}
	}
	after, readErr := os.ReadFile(stable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatal("runner failure changed stable jar")
	}
	if _, statErr := os.Stat(tempPath); !os.IsNotExist(statErr) {
		t.Fatalf("temporary jar still exists after failure: %v", statErr)
	}
	assertNoBrowserTemps(t, dir)
}

func TestBrowserSyncRejectsAnonymousCandidate(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	before := []byte(sampleNetscape(true) + "# strong-stable\n")
	if err := os.WriteFile(stable, before, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := browserRunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		path, _ := browserArgValue(args, "--cookies")
		return "", "", os.WriteFile(path, []byte(anonymousNetscape()), 0o600)
	})

	_, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: "chrome:Default",
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Timeout:     time.Second,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not logged in") {
		t.Fatalf("expected logged-out candidate rejection, got %v", err)
	}
	after, readErr := os.ReadFile(stable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatal("anonymous candidate overwrote stable login jar")
	}
}

func TestBrowserSyncUpgradesAnonymousStable(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(anonymousNetscape()), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := browserRunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		path, _ := browserArgValue(args, "--cookies")
		return "", "", os.WriteFile(path, []byte(sampleNetscape(true)), 0o600)
	})

	result, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: "chrome:Default",
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || !result.LoggedIn {
		t.Fatalf("expected anonymous jar upgrade, got %+v", result)
	}
}

func TestBrowserSyncKeepsStrongerLoggedInStable(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	strong := sampleNetscape(true)
	for i := 0; i < 8; i++ {
		strong += fmt.Sprintf(".youtube.com\tTRUE\t/\tFALSE\t0\tEXTRA_%d\tvalue%d\n", i, i)
	}
	if err := os.WriteFile(stable, []byte(strong), 0o600); err != nil {
		t.Fatal(err)
	}
	weakLogin := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tlogin\n"
	runner := browserRunnerFunc(func(_ context.Context, _ string, args []string) (string, string, error) {
		path, _ := browserArgValue(args, "--cookies")
		return "", "", os.WriteFile(path, []byte(weakLogin), 0o600)
	})

	result, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: "chrome:Default",
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated || !result.LoggedIn || result.StableScore <= result.CandidateScore {
		t.Fatalf("stronger stable jar should remain: %+v", result)
	}
	body, readErr := os.ReadFile(stable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != strong {
		t.Fatal("weaker logged-in candidate replaced stronger stable jar")
	}
}

func TestBrowserSyncTimeoutCleansTemp(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	var tempPath string
	runner := browserRunnerFunc(func(ctx context.Context, _ string, args []string) (string, string, error) {
		tempPath, _ = browserArgValue(args, "--cookies")
		<-ctx.Done()
		return "sensitive stdout", "sensitive stderr", ctx.Err()
	})

	_, err := NewBrowserSyncer(runner).Sync(context.Background(), BrowserSyncOptions{
		BrowserSpec: "chrome:Default",
		StableFile:  stable,
		YtdlpPath:   "yt-dlp-test",
		Timeout:     20 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if _, statErr := os.Stat(tempPath); !os.IsNotExist(statErr) {
		t.Fatalf("temporary jar still exists after timeout: %v", statErr)
	}
	assertNoBrowserTemps(t, dir)
}

type serialBrowserRunner struct {
	active    atomic.Int32
	maxActive atomic.Int32
}

func (r *serialBrowserRunner) Run(_ context.Context, _ string, args []string) (string, string, error) {
	active := r.active.Add(1)
	defer r.active.Add(-1)
	for {
		maximum := r.maxActive.Load()
		if active <= maximum || r.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	path, ok := browserArgValue(args, "--cookies")
	if !ok {
		return "", "", errors.New("missing cookies argument")
	}
	return "", "", os.WriteFile(path, []byte(sampleNetscape(true)), 0o600)
}

func TestBrowserSyncerSerializesConcurrentCalls(t *testing.T) {
	dir := t.TempDir()
	runner := &serialBrowserRunner{}
	syncer := NewBrowserSyncer(runner)
	start := make(chan struct{})
	errCh := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := syncer.Sync(context.Background(), BrowserSyncOptions{
				BrowserSpec: "chrome:Default",
				StableFile:  filepath.Join(dir, StableFileName),
				YtdlpPath:   "yt-dlp-test",
				Timeout:     time.Second,
			})
			if err != nil {
				errCh <- fmt.Errorf("call %d: %w", index, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := runner.maxActive.Load(); got != 1 {
		t.Fatalf("runner max concurrent calls=%d, want 1", got)
	}
	assertNoBrowserTemps(t, dir)
}

func TestClassifyBrowserCommandErrorUsesBoundedCategories(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "profile", output: "could not find chrome cookies database at SECRET_PATH", want: "unavailable"},
		{name: "decrypt", output: "failed to decrypt cookie SECRET_VALUE", want: "decrypted"},
		{name: "generic", output: "unexpected SECRET_DETAIL", want: "extraction failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyBrowserCommandError("", tt.output, errors.New("exit status 1"))
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%q, want category %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("classified error leaked raw output: %v", err)
			}
		})
	}
}

func assertNoBrowserTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".browser-cookies-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("browser temp files remain: %v", matches)
	}
}
