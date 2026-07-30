package managedlogin

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

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

type fakeLauncher struct {
	mu       sync.Mutex
	browsers []*fakeBrowser
	options  []LaunchOptions
	err      error
}

func (l *fakeLauncher) Launch(_ context.Context, opt LaunchOptions) (Browser, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.options = append(l.options, opt)
	if l.err != nil {
		return nil, l.err
	}
	var browser *fakeBrowser
	if len(l.browsers) > 0 {
		browser = l.browsers[0]
		l.browsers = l.browsers[1:]
	} else {
		browser = newFakeBrowser(nil)
	}
	browser.mu.Lock()
	browser.viewport = opt.Viewport
	browser.mu.Unlock()
	return browser, nil
}

func (l *fakeLauncher) launchOptions() []LaunchOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LaunchOptions(nil), l.options...)
}

type fakeBrowser struct {
	mu         sync.Mutex
	frames     chan Frame
	done       chan error
	closeOnce  sync.Once
	viewport   Viewport
	cookies    []BrowserCookie
	exportErr  error
	dispatched []InputEvent
	clearCalls int
	closeCalls int
}

func newFakeBrowser(browserCookies []BrowserCookie) *fakeBrowser {
	return &fakeBrowser{
		frames:   make(chan Frame, 1),
		done:     make(chan error, 1),
		viewport: Viewport{Width: defaultWidth, Height: defaultHeight},
		cookies:  append([]BrowserCookie(nil), browserCookies...),
	}
}

func (b *fakeBrowser) Frames() <-chan Frame { return b.frames }
func (b *fakeBrowser) Done() <-chan error   { return b.done }
func (b *fakeBrowser) Viewport() Viewport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.viewport
}
func (b *fakeBrowser) Dispatch(_ context.Context, event InputEvent) error {
	b.mu.Lock()
	b.dispatched = append(b.dispatched, event)
	b.mu.Unlock()
	return nil
}
func (b *fakeBrowser) Resize(_ context.Context, viewport Viewport) error {
	b.mu.Lock()
	b.viewport = viewport
	b.mu.Unlock()
	return nil
}
func (b *fakeBrowser) ExportCookies(context.Context) ([]BrowserCookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exportErr != nil {
		return nil, b.exportErr
	}
	return append([]BrowserCookie(nil), b.cookies...), nil
}
func (b *fakeBrowser) ClearCookies(context.Context) error {
	b.mu.Lock()
	b.clearCalls++
	b.cookies = nil
	b.mu.Unlock()
	return nil
}
func (b *fakeBrowser) Close(context.Context) error {
	b.mu.Lock()
	b.closeCalls++
	b.mu.Unlock()
	b.closeOnce.Do(func() {
		b.done <- nil
		close(b.done)
	})
	return nil
}
func (b *fakeBrowser) crash() {
	b.closeOnce.Do(func() {
		b.done <- errors.New("raw browser crash detail")
		close(b.done)
	})
}

func TestManagerMutualExclusionTerminationAndProfileReuse(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	stableDir := t.TempDir()
	stable := filepath.Join(stableDir, "youtube.txt")
	launcher := &fakeLauncher{browsers: []*fakeBrowser{newFakeBrowser(nil), newFakeBrowser(nil)}}
	m := newTestManager(t, profile, stable, launcher, 2*time.Second, nil, nil)
	defer m.Close()
	owner := strings.Repeat("a", 64)

	first, err := m.Create(owner)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, owner, first.ID, StateInteractive)
	if _, err := m.Create(owner); !errors.Is(err, ErrBusy) {
		t.Fatalf("second create err=%v", err)
	}
	if _, err := m.Terminate(owner, first.ID); err != nil {
		t.Fatal(err)
	}

	second, err := m.Create(owner)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, owner, second.ID, StateInteractive)
	options := launcher.launchOptions()
	if len(options) != 2 || options[0].ProfileDir != profile || options[1].ProfileDir != profile {
		t.Fatalf("launch profile options=%+v", options)
	}
}

func TestManagerSessionTTLExpiresAndClosesBrowser(t *testing.T) {
	browser := newFakeBrowser(nil)
	launcher := &fakeLauncher{browsers: []*fakeBrowser{browser, newFakeBrowser(nil)}}
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "youtube.txt"), launcher, 50*time.Millisecond, nil, nil)
	defer m.Close()
	owner := strings.Repeat("b", 64)
	snapshot, err := m.Create(owner)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	waitState(t, m, owner, snapshot.ID, StateExpired)
	browser.mu.Lock()
	closeCalls := browser.closeCalls
	browser.mu.Unlock()
	if closeCalls == 0 {
		t.Fatal("expired browser was not closed")
	}
	if _, err := m.Create(owner); err != nil {
		t.Fatalf("new session after expiry: %v", err)
	}
}

func TestManagerInputActivityExtendsIdleTTLWithinHardLimit(t *testing.T) {
	browser := newFakeBrowser(nil)
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "youtube.txt"), &fakeLauncher{browsers: []*fakeBrowser{browser}}, 120*time.Millisecond, nil, nil)
	defer m.Close()
	owner := strings.Repeat("5", 64)
	snapshot, _ := m.Create(owner)
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	control, err := m.AcquireControl(owner, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	time.Sleep(75 * time.Millisecond)
	if err := control.Dispatch(context.Background(), InputEvent{Kind: "mouse", EventType: "mouseMoved", X: 0.5, Y: 0.5}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	current, err := m.Get(owner, snapshot.ID)
	if err != nil || current.State != StateInteractive {
		t.Fatalf("activity did not extend idle TTL: state=%+v err=%v", current, err)
	}
	waitState(t, m, owner, snapshot.ID, StateExpired)
}

func TestManagerVerifyCommitsAuthenticatedJarAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, "youtube.txt")
	browser := newFakeBrowser(authenticatedBrowserCookies())
	launcher := &fakeLauncher{browsers: []*fakeBrowser{browser}}
	source := cookies.NewSourceArbiter(cookies.CookieSourceModeAuto, true)
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), stable, launcher, time.Second, source, nil)
	defer m.Close()
	owner := strings.Repeat("c", 64)
	snapshot, err := m.Create(owner)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	verified, err := m.Verify(owner, snapshot.ID)
	if err != nil {
		t.Fatalf("verify: %v snapshot=%+v", err, verified)
	}
	if verified.State != StateSynced || !verified.LoggedIn || !verified.Updated || verified.AuthCookieCount < 2 {
		t.Fatalf("verified=%+v", verified)
	}
	status, err := cookies.InspectCookieFileStatus(stable)
	if err != nil || !status.Present || !status.Quality.LoggedIn {
		t.Fatalf("stable status=%+v err=%v", status, err)
	}
	if !source.ManagedAuthenticated() || source.SelectedSource(true) != cookies.CookieSourceManaged {
		t.Fatal("managed source was not selected")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".managed-cookies-") {
			t.Fatalf("temporary jar remained: %s", entry.Name())
		}
	}
}

func TestManagerNotLoggedInPreservesStableJar(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, "youtube.txt")
	original := authenticatedNetscape("old-login")
	if err := os.WriteFile(stable, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	browser := newFakeBrowser([]BrowserCookie{{
		Name: "VISITOR_INFO1_LIVE", Value: "visitor", Domain: ".youtube.com", Path: "/", Secure: true,
	}})
	source := cookies.NewSourceArbiter(cookies.CookieSourceModeAuto, true)
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), stable, &fakeLauncher{browsers: []*fakeBrowser{browser}}, time.Second, source, nil)
	defer m.Close()
	owner := strings.Repeat("d", 64)
	snapshot, _ := m.Create(owner)
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	got, err := m.Verify(owner, snapshot.ID)
	if !errors.Is(err, ErrNotLoggedIn) || got.State != StateNotLoggedIn {
		t.Fatalf("verify err=%v snapshot=%+v", err, got)
	}
	after, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatal("anonymous candidate replaced authenticated stable jar")
	}
	if source.ManagedAuthenticated() {
		t.Fatal("anonymous profile marked authenticated")
	}
}

func TestManagerOwnerIsolationSingleControlAndSensitiveInputNotLogged(t *testing.T) {
	var logMu sync.Mutex
	var logs strings.Builder
	logf := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		logs.WriteString(format)
		for _, arg := range args {
			logs.WriteString("|")
			logs.WriteString(strings.TrimSpace(toString(arg)))
		}
	}
	browser := newFakeBrowser(nil)
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "youtube.txt"), &fakeLauncher{browsers: []*fakeBrowser{browser}}, time.Second, nil, logf)
	defer m.Close()
	owner := strings.Repeat("e", 64)
	other := strings.Repeat("f", 64)
	snapshot, _ := m.Create(owner)
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	if _, err := m.Get(other, snapshot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner get err=%v", err)
	}
	control, err := m.AcquireControl(owner, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcquireControl(owner, snapshot.ID); !errors.Is(err, ErrControlBusy) {
		t.Fatalf("duplicate control err=%v", err)
	}
	secret := "secret-password-123"
	if err := control.Dispatch(context.Background(), InputEvent{Kind: "text", Text: secret}); err != nil {
		t.Fatal(err)
	}
	control.Close()
	if _, err := m.AcquireControl(owner, snapshot.ID); err != nil {
		t.Fatalf("control should be reusable after release: %v", err)
	}
	logMu.Lock()
	logged := logs.String()
	logMu.Unlock()
	if strings.Contains(logged, secret) {
		t.Fatalf("sensitive input entered logs: %q", logged)
	}
}

func TestManagerBrowserCrashCleansSessionAndAllowsNext(t *testing.T) {
	firstBrowser := newFakeBrowser(nil)
	launcher := &fakeLauncher{browsers: []*fakeBrowser{firstBrowser, newFakeBrowser(nil)}}
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "youtube.txt"), launcher, time.Second, nil, nil)
	defer m.Close()
	owner := strings.Repeat("1", 64)
	first, _ := m.Create(owner)
	waitState(t, m, owner, first.ID, StateInteractive)
	firstBrowser.crash()
	waitState(t, m, owner, first.ID, StateClosed)
	closed, err := m.Get(owner, first.ID)
	if err != nil || closed.Error != ErrorBrowserClosed {
		t.Fatalf("closed=%+v err=%v", closed, err)
	}
	if _, err := m.Create(owner); err != nil {
		t.Fatalf("create after crash: %v", err)
	}
}

func TestManagerRestartUsesSamePersistentProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "persistent-profile")
	stableDir := t.TempDir()
	stable := filepath.Join(stableDir, "youtube.txt")
	owner := strings.Repeat("2", 64)
	firstLauncher := &fakeLauncher{browsers: []*fakeBrowser{newFakeBrowser(nil)}}
	first := newTestManager(t, profile, stable, firstLauncher, time.Second, nil, nil)
	s1, _ := first.Create(owner)
	waitState(t, first, owner, s1.ID, StateInteractive)
	_, _ = first.Terminate(owner, s1.ID)
	first.Close()

	secondLauncher := &fakeLauncher{browsers: []*fakeBrowser{newFakeBrowser(nil)}}
	second := newTestManager(t, profile, stable, secondLauncher, time.Second, nil, nil)
	defer second.Close()
	s2, _ := second.Create(owner)
	waitState(t, second, owner, s2.ID, StateInteractive)
	firstOpts := firstLauncher.launchOptions()
	secondOpts := secondLauncher.launchOptions()
	if len(firstOpts) != 1 || len(secondOpts) != 1 || firstOpts[0].ProfileDir != secondOpts[0].ProfileDir {
		t.Fatalf("profiles first=%+v second=%+v", firstOpts, secondOpts)
	}
}

func TestManagerDisconnectClearsManagedProfileAndStableJar(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stableDir := t.TempDir()
	stable := filepath.Join(stableDir, "youtube.txt")
	interactive := newFakeBrowser(authenticatedBrowserCookies())
	disconnectBrowser := newFakeBrowser(authenticatedBrowserCookies())
	launcher := &fakeLauncher{browsers: []*fakeBrowser{interactive, disconnectBrowser}}
	source := cookies.NewSourceArbiter(cookies.CookieSourceModeAuto, false)
	m := newTestManager(t, profile, stable, launcher, time.Second, source, nil)
	defer m.Close()
	owner := strings.Repeat("3", 64)
	snapshot, _ := m.Create(owner)
	waitState(t, m, owner, snapshot.ID, StateInteractive)
	if _, err := m.Verify(owner, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	fallback := authenticatedNetscape("fallback-login")
	if err := os.WriteFile(filepath.Join(stableDir, "fallback.txt"), []byte(fallback), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Disconnect(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	disconnectBrowser.mu.Lock()
	clearCalls := disconnectBrowser.clearCalls
	disconnectBrowser.mu.Unlock()
	if clearCalls != 1 {
		t.Fatalf("clear calls=%d", clearCalls)
	}
	if source.ManagedAuthenticated() {
		t.Fatal("managed credentials remained active after disconnect")
	}
	after, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != fallback {
		t.Fatal("file fallback was not promoted after managed disconnect")
	}
}

func TestManagerLaunchFailureDoesNotExposeRawErrorInLogs(t *testing.T) {
	secret := "raw-cdp-secret-token"
	var logMu sync.Mutex
	var logs strings.Builder
	m := newTestManager(t, filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "youtube.txt"), &fakeLauncher{err: errors.New(secret)}, 200*time.Millisecond, nil, func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		logs.WriteString(format)
		for _, arg := range args {
			logs.WriteString(toString(arg))
		}
	})
	defer m.Close()
	owner := strings.Repeat("4", 64)
	snapshot, _ := m.Create(owner)
	waitState(t, m, owner, snapshot.ID, StateClosed)
	logMu.Lock()
	logged := logs.String()
	logMu.Unlock()
	if strings.Contains(logged, secret) {
		t.Fatalf("raw launch error entered logs: %q", logged)
	}
}

func TestManagerRejectsUnsafeProfileDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Options{
		ProfileDir: cwd,
		StableFile: filepath.Join(t.TempDir(), "youtube.txt"),
	})
	if err == nil {
		t.Fatal("working directory must not be accepted as a managed profile")
	}
}

func newTestManager(t *testing.T, profile, stable string, launcher Launcher, ttl time.Duration, source SourceCoordinator, logf func(string, ...any)) *Manager {
	t.Helper()
	m, err := New(Options{
		ProfileDir: profile,
		StableFile: stable,
		Headless:   true,
		SessionTTL: ttl,
		Launcher:   launcher,
		Source:     source,
		Logf:       logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitState(t *testing.T, m *Manager, owner, id, want string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := m.Get(owner, id)
		if snapshot.State == want {
			return snapshot
		}
		if err != nil && !errors.Is(err, ErrExpired) {
			t.Fatalf("get state %s: %v", want, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, _ := m.Get(owner, id)
	t.Fatalf("state=%q want=%q snapshot=%+v", snapshot.State, want, snapshot)
	return Snapshot{}
}

func authenticatedBrowserCookies() []BrowserCookie {
	return []BrowserCookie{
		{Name: "LOGIN_INFO", Value: "login", Domain: ".youtube.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SID", Value: "sid", Domain: ".google.com", Path: "/", HTTPOnly: true, Secure: true},
		{Name: "SAPISID", Value: "sapisid", Domain: ".google.com", Path: "/", Secure: true},
		{Name: "VISITOR_INFO1_LIVE", Value: "visitor", Domain: ".youtube.com", Path: "/", Secure: true},
		{Name: "ignored", Value: "other", Domain: ".example.com", Path: "/"},
	}
}

func authenticatedNetscape(loginValue string) string {
	return "# Netscape HTTP Cookie File\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\t" + loginValue + "\n" +
		"#HttpOnly_.google.com\tTRUE\t/\tTRUE\t0\tSID\tsid\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSAPISID\tsapisid\n"
}

func toString(value any) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprint(value), "\n", ""), "\r", ""))
}
