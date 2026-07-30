package cookies

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInspectCookieFileStatusReturnsQualityMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StableFileName)
	body := sampleNetscape(true)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := InspectCookieFileStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Present || status.SizeBytes != int64(len(body)) || status.ModifiedAt.IsZero() {
		t.Fatalf("file metadata mismatch: %+v", status)
	}
	if !status.Quality.Valid || !status.Quality.LoggedIn || status.Quality.CookieCount == 0 || status.Quality.AuthCookies == 0 {
		t.Fatalf("quality metadata mismatch: %+v", status.Quality)
	}
	if status.ModifiedAt.Location() != time.UTC {
		t.Fatalf("modified time must be UTC: %v", status.ModifiedAt.Location())
	}
}

func TestCookieLifecycleStatusTracksUpdatedBrowserSync(t *testing.T) {
	fake := &lifecycleBrowserFake{
		run: func(_ context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			return BrowserSyncResult{Updated: true, LoggedIn: true, CookieCount: 7}, nil
		},
	}
	lifecycle := NewCookieLifecycle(fake, nil, nil)
	before := time.Now().UTC()
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		BrowserSpec:        `chrome:C:\Users\SECRET_PROFILE\Default`,
		BrowserSyncOnStart: true,
	})
	after := time.Now().UTC()
	status := lifecycle.CookieSyncStatus()
	if !status.BrowserConfigured || status.InProgress {
		t.Fatalf("configuration/progress mismatch: %+v", status)
	}
	if status.LastPhase != CookieSyncPhaseStartup || status.LastResult != CookieSyncResultUpdated ||
		status.LastError != "" || !status.LastUpdated {
		t.Fatalf("updated status mismatch: %+v", status)
	}
	if status.LastSyncAt.Before(before) || status.LastSyncAt.After(after) || !status.LastSuccessAt.Equal(status.LastSyncAt) {
		t.Fatalf("sync timestamps mismatch: %+v", status)
	}
	if strings.Contains(strings.Join([]string{status.LastPhase, status.LastResult, status.LastError}, " "), "SECRET_PROFILE") {
		t.Fatal("status leaked browser profile")
	}
}

func TestCookieLifecycleStatusMapsFailureToBoundedSummary(t *testing.T) {
	fake := &lifecycleBrowserFake{
		run: func(_ context.Context, _ BrowserSyncOptions, _ int) (BrowserSyncResult, error) {
			return BrowserSyncResult{}, errors.New("database is locked at SECRET_PROFILE with COOKIE_SECRET")
		},
	}
	lifecycle := NewCookieLifecycle(fake, nil, nil)
	lifecycle.RunStartup(context.Background(), CookieLifecycleOptions{
		BrowserSpec:        "chrome:Default",
		BrowserSyncOnStart: true,
	})
	status := lifecycle.CookieSyncStatus()
	if status.LastResult != CookieSyncResultFailed || status.LastError != CookieSyncErrorProfileLocked || status.LastUpdated {
		t.Fatalf("failure status mismatch: %+v", status)
	}
	if status.LastSyncAt.IsZero() || !status.LastSuccessAt.IsZero() {
		t.Fatalf("failure timestamps mismatch: %+v", status)
	}
	if strings.Contains(status.LastError, "SECRET") || strings.Contains(status.LastError, "COOKIE") {
		t.Fatalf("failure summary leaked raw error: %q", status.LastError)
	}
}

func TestCookieLifecycleStatusReportsInProgressThenCancellation(t *testing.T) {
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.RunStartup(ctx, CookieLifecycleOptions{
			BrowserSpec:        "chrome:Default",
			BrowserSyncOnStart: true,
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("browser sync did not start")
	}
	inProgress := lifecycle.CookieSyncStatus()
	if !inProgress.InProgress || inProgress.LastPhase != CookieSyncPhaseStartup || inProgress.LastResult != CookieSyncResultNever {
		t.Fatalf("in-progress status mismatch: %+v", inProgress)
	}
	cancel()
	waitDone(t, done)
	finished := lifecycle.CookieSyncStatus()
	if finished.InProgress || finished.LastResult != CookieSyncResultCanceled || finished.LastError != CookieSyncErrorCanceled {
		t.Fatalf("canceled status mismatch: %+v", finished)
	}
}

func TestSanitizeCookieSyncStatusRejectsArbitraryProviderText(t *testing.T) {
	status := SanitizeCookieSyncStatus(CookieSyncStatus{
		BrowserConfigured: true,
		LastPhase:         "SECRET_PHASE",
		LastResult:        "SECRET_RESULT",
		LastError:         "SECRET_PROFILE COOKIE_VALUE",
		LastUpdated:       true,
		LastSyncAt:        time.Now(),
	})
	if status.LastPhase != "" || status.LastResult != CookieSyncResultFailed ||
		status.LastError != CookieSyncErrorGeneric || status.LastUpdated {
		t.Fatalf("arbitrary provider status was not bounded: %+v", status)
	}
}

func TestSummarizeBrowserSyncErrorUsesFixedCategories(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "timeout", err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), want: CookieSyncErrorTimeout},
		{name: "canceled", err: context.Canceled, want: CookieSyncErrorCanceled},
		{name: "locked", err: errors.New("cookies: browser profile database is locked at SECRET"), want: CookieSyncErrorProfileLocked},
		{name: "decrypt", err: errors.New("cookies: browser profile cookies could not be decrypted SECRET"), want: CookieSyncErrorProfileDecrypt},
		{name: "profile", err: errors.New("cookies: browser profile is unavailable SECRET"), want: CookieSyncErrorProfileUnavailable},
		{name: "login", err: errors.New("cookies: browser profile is not logged in SECRET"), want: CookieSyncErrorNotLoggedIn},
		{name: "youtube", err: errors.New("cookies: browser extraction has no YouTube or Google cookies SECRET"), want: CookieSyncErrorMissingYouTubeCookie},
		{name: "jar", err: errors.New("cookies: browser extraction produced an invalid jar SECRET"), want: CookieSyncErrorInvalidJar},
		{name: "ytdlp", err: errors.New("cookies: yt-dlp executable is unavailable SECRET"), want: CookieSyncErrorYtdlpUnavailable},
		{name: "temp", err: errors.New("cookies: prepare browser cookie jar failed SECRET"), want: CookieSyncErrorTemporaryFile},
		{name: "commit", err: errors.New("cookies: commit synchronized jar failed SECRET"), want: CookieSyncErrorCommit},
		{name: "config", err: errors.New("cookies: browser source is empty SECRET"), want: CookieSyncErrorConfiguration},
		{name: "generic", err: errors.New("arbitrary SECRET_PROFILE COOKIE_VALUE"), want: CookieSyncErrorGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeBrowserSyncError(tt.err)
			if got != tt.want {
				t.Fatalf("summary=%q want %q", got, tt.want)
			}
			if strings.Contains(got, "SECRET") || strings.Contains(got, "COOKIE_VALUE") {
				t.Fatalf("summary leaked raw error: %q", got)
			}
		})
	}
}
