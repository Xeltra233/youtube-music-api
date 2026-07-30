package managedlogin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

const (
	defaultSessionTTL = 15 * time.Minute
	maxSessionWindow  = 30 * time.Minute
	defaultWidth      = 1280
	defaultHeight     = 800
	loginURL          = "https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fwww.youtube.com%2F"
	refreshURL        = "https://www.youtube.com/"
)

type loginSession struct {
	id          string
	owner       string
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	doneOnce    sync.Once
	createdAt   time.Time
	expiresAt   time.Time
	hardExpires time.Time
	state       string
	errorCode   string
	viewport    Viewport
	browser     Browser
	control     bool
	loggedIn    bool
	quality     VerifyResult
}

// Manager owns one dedicated persistent profile and at most one live
// interactive session. The profile mutex spans the full Chromium process
// lifetime, preventing refresh, disconnect, and interactive browsers from
// opening the same profile concurrently.
type Manager struct {
	mu        sync.Mutex
	profileMu sync.Mutex
	rootCtx   context.Context
	cancel    context.CancelFunc
	opt       Options
	launcher  Launcher
	active    *loginSession
	closed    bool
}

// New creates a managed login coordinator without launching a browser.
func New(opt Options) (*Manager, error) {
	root := opt.Context
	if root == nil {
		root = context.Background()
	}
	root, cancel := context.WithCancel(root)

	profile := strings.TrimSpace(opt.ProfileDir)
	if profile == "" {
		cancel()
		return nil, fmt.Errorf("managedlogin: empty profile dir")
	}
	absProfile, err := filepath.Abs(profile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("managedlogin: resolve profile dir: %w", err)
	}
	if unsafeProfileDir(absProfile) {
		cancel()
		return nil, fmt.Errorf("managedlogin: profile dir must be a dedicated subdirectory")
	}
	stable := strings.TrimSpace(opt.StableFile)
	if stable == "" {
		cancel()
		return nil, fmt.Errorf("managedlogin: empty stable cookie file")
	}
	absStable, err := filepath.Abs(stable)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("managedlogin: resolve stable cookie file: %w", err)
	}

	if opt.SessionTTL <= 0 {
		opt.SessionTTL = defaultSessionTTL
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	if opt.Launcher == nil {
		opt.Launcher = NewCDPLauncher()
	}
	opt.ProfileDir = absProfile
	opt.StableFile = absStable
	ctx := root
	return &Manager{
		rootCtx:  ctx,
		cancel:   cancel,
		opt:      opt,
		launcher: opt.Launcher,
	}, nil
}

// Create reserves the profile for one admin session and starts Chromium in the
// background. The response can therefore expose starting before interactive.
func (m *Manager) Create(owner string) (Snapshot, error) {
	if m == nil || !m.managedEnabled() {
		return Snapshot{}, ErrDisabled
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Snapshot{}, ErrNotFound
	}
	id, err := newSessionID()
	if err != nil {
		return Snapshot{}, ErrSyncFailed
	}
	now := m.opt.Now().UTC()
	ttl := m.opt.SessionTTL
	hardWindow := ttl * 2
	if hardWindow < ttl {
		hardWindow = ttl
	}
	if hardWindow > maxSessionWindow {
		hardWindow = maxSessionWindow
	}
	if hardWindow <= 0 {
		hardWindow = ttl
	}
	hardExpires := now.Add(hardWindow)
	expiresAt := now.Add(ttl)
	if expiresAt.After(hardExpires) {
		expiresAt = hardExpires
	}
	ctx, cancel := context.WithCancel(m.rootCtx)
	s := &loginSession{
		id:          id,
		owner:       owner,
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		createdAt:   now,
		expiresAt:   expiresAt,
		hardExpires: hardExpires,
		state:       StateStarting,
		viewport:    Viewport{Width: defaultWidth, Height: defaultHeight},
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return Snapshot{}, ErrDisabled
	}
	if m.active != nil && sessionLive(m.active.state) {
		m.mu.Unlock()
		cancel()
		return Snapshot{}, ErrBusy
	}
	m.active = s
	snap := snapshotLocked(s)
	m.mu.Unlock()

	m.log("youtube login: session=%s state=%s", shortID(id), StateStarting)
	go m.runSession(s)
	go m.watchExpiry(s)
	return snap, nil
}

func (m *Manager) runSession(s *loginSession) {
	m.profileMu.Lock()
	defer m.profileMu.Unlock()

	if err := ensureProfileDir(m.opt.ProfileDir); err != nil {
		m.failStart(s, ErrorBrowserStart)
		return
	}
	browser, err := m.launcher.Launch(s.ctx, LaunchOptions{
		ExecutablePath: m.opt.BrowserPath,
		ProfileDir:     m.opt.ProfileDir,
		Headless:       m.opt.Headless,
		Viewport:       s.viewport,
		StartURL:       loginURL,
		Screencast:     true,
	})
	if err != nil || browser == nil {
		code := ErrorBrowserStart
		if errors.Is(err, os.ErrNotExist) {
			code = ErrorBrowserUnavailable
		}
		m.failStart(s, code)
		return
	}

	m.mu.Lock()
	if m.active != s || !sessionLive(s.state) || s.ctx.Err() != nil {
		m.mu.Unlock()
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = browser.Close(closeCtx)
		cancel()
		s.doneOnce.Do(func() { close(s.done) })
		return
	}
	s.browser = browser
	if vp := browser.Viewport(); validViewport(vp) {
		s.viewport = vp
	}
	s.state = StateInteractive
	s.errorCode = ""
	m.touchLocked(s)
	m.mu.Unlock()
	m.log("youtube login: session=%s state=%s", shortID(s.id), StateInteractive)

	var browserErr error
	select {
	case browserErr = <-browser.Done():
	case <-s.ctx.Done():
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = browser.Close(closeCtx)
		cancel()
		select {
		case browserErr = <-browser.Done():
		case <-time.After(3 * time.Second):
		}
	}

	m.mu.Lock()
	if m.active == s {
		s.browser = nil
		if sessionLive(s.state) {
			if !m.opt.Now().UTC().Before(s.expiresAt) {
				s.state = StateExpired
				s.errorCode = ErrorSessionExpired
			} else {
				s.state = StateClosed
				s.errorCode = ErrorBrowserClosed
			}
		}
	}
	state := s.state
	m.mu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
	_ = browserErr // Raw browser errors are intentionally discarded.
	m.log("youtube login: session=%s state=%s", shortID(s.id), state)
}

func (m *Manager) failStart(s *loginSession, code string) {
	m.mu.Lock()
	if m.active == s && sessionLive(s.state) {
		s.state = StateClosed
		s.errorCode = code
		s.cancel()
	}
	m.mu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
	m.log("youtube login: session=%s state=%s error=%s", shortID(s.id), StateClosed, code)
}

func (m *Manager) watchExpiry(s *loginSession) {
	interval := m.opt.SessionTTL / 4
	if interval > time.Second {
		interval = time.Second
	}
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			now := m.opt.Now().UTC()
			m.mu.Lock()
			if m.active != s || !sessionLive(s.state) {
				m.mu.Unlock()
				return
			}
			if now.Before(s.expiresAt) {
				m.mu.Unlock()
				continue
			}
			s.state = StateExpired
			s.errorCode = ErrorSessionExpired
			s.control = false
			s.cancel()
			browser := s.browser
			m.mu.Unlock()
			if browser != nil {
				closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = browser.Close(closeCtx)
				cancel()
			}
			m.log("youtube login: session=%s state=%s", shortID(s.id), StateExpired)
			return
		}
	}
}

// Get returns a session only to the admin session that created it.
func (m *Manager) Get(owner, id string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.lookupLocked(owner, id)
	if err != nil {
		return Snapshot{}, err
	}
	m.syncViewportLocked(s)
	if s.state == StateExpired {
		return snapshotLocked(s), ErrExpired
	}
	return snapshotLocked(s), nil
}

// Terminate closes the browser while retaining a terminal metadata snapshot.
func (m *Manager) Terminate(owner, id string) (Snapshot, error) {
	m.mu.Lock()
	s, err := m.lookupLocked(owner, id)
	if err != nil {
		m.mu.Unlock()
		return Snapshot{}, err
	}
	if s.state != StateSynced && s.state != StateExpired {
		s.state = StateClosed
		s.errorCode = ""
	}
	s.control = false
	s.cancel()
	browser := s.browser
	done := s.done
	snap := snapshotLocked(s)
	m.mu.Unlock()
	if browser != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = browser.Close(closeCtx)
		cancel()
	}
	select {
	case <-done:
	case <-time.After(4 * time.Second):
	}
	return snap, nil
}

// Verify exports cookies through CDP, validates authenticated quality, and
// atomically installs the candidate stable jar.
func (m *Manager) Verify(owner, id string) (Snapshot, error) {
	m.mu.Lock()
	s, err := m.lookupLocked(owner, id)
	if err != nil {
		m.mu.Unlock()
		return Snapshot{}, err
	}
	if s.state == StateVerifying || s.state == StateAuthenticated {
		m.mu.Unlock()
		return Snapshot{}, ErrVerifyBusy
	}
	if s.state == StateExpired {
		snap := snapshotLocked(s)
		m.mu.Unlock()
		return snap, ErrExpired
	}
	if s.browser == nil || !verifyAllowed(s.state) {
		m.mu.Unlock()
		return Snapshot{}, ErrNotReady
	}
	s.state = StateVerifying
	s.errorCode = ""
	m.touchLocked(s)
	browser := s.browser
	m.mu.Unlock()

	verifyCtx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	result, verifyErr := m.exportAndCommit(verifyCtx, browser)
	cancel()

	m.mu.Lock()
	if m.active != s {
		m.mu.Unlock()
		return Snapshot{}, ErrNotFound
	}
	if s.state == StateExpired {
		snap := snapshotLocked(s)
		m.mu.Unlock()
		return snap, ErrExpired
	}
	if s.ctx.Err() != nil && s.state != StateVerifying {
		snap := snapshotLocked(s)
		m.mu.Unlock()
		return snap, ErrBrowserClosed
	}
	s.quality = result
	s.loggedIn = result.LoggedIn
	if verifyErr != nil {
		switch {
		case errors.Is(verifyErr, ErrNotLoggedIn):
			s.state = StateNotLoggedIn
			s.errorCode = ErrorNotLoggedIn
		case errors.Is(verifyErr, errStablePreserved):
			s.state = StateSyncFailed
			s.errorCode = ErrorStablePreserved
		case errors.Is(verifyErr, ErrBrowserProtocol), errors.Is(verifyErr, ErrBrowserClosed):
			s.state = StateSyncFailed
			s.errorCode = ErrorCookieExport
		default:
			s.state = StateSyncFailed
			s.errorCode = ErrorCookieCommit
		}
		m.touchLocked(s)
		snap := snapshotLocked(s)
		m.mu.Unlock()
		if errors.Is(verifyErr, ErrNotLoggedIn) {
			return snap, ErrNotLoggedIn
		}
		return snap, ErrSyncFailed
	}

	s.state = StateAuthenticated
	s.errorCode = ""
	s.state = StateSynced
	s.control = false
	s.cancel()
	snap := snapshotLocked(s)
	m.mu.Unlock()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = browser.Close(closeCtx)
	closeCancel()
	m.log(
		"youtube login: session=%s state=%s updated=%t logged_in=%t cookies=%d score=%d",
		shortID(s.id), StateSynced, result.Updated, result.LoggedIn, result.CookieCount, result.QualityScore,
	)
	return snap, nil
}

// AcquireControl grants the sole interactive client lease for a session.
func (m *Manager) AcquireControl(owner, id string) (*Control, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.lookupLocked(owner, id)
	if err != nil {
		return nil, err
	}
	if s.state == StateExpired {
		return nil, ErrExpired
	}
	if s.browser == nil || !sessionLive(s.state) {
		return nil, ErrNotReady
	}
	if s.control {
		return nil, ErrControlBusy
	}
	s.control = true
	m.touchLocked(s)
	return &Control{manager: m, session: s, browser: s.browser}, nil
}

// Control is the single WebSocket-facing browser lease.
type Control struct {
	manager *Manager
	session *loginSession
	browser Browser
	once    sync.Once
}

func (c *Control) Frames() <-chan Frame {
	if c == nil || c.browser == nil {
		ch := make(chan Frame)
		close(ch)
		return ch
	}
	return c.browser.Frames()
}

func (c *Control) Done() <-chan struct{} {
	if c == nil || c.session == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return c.session.done
}

func (c *Control) Snapshot() (Snapshot, error) {
	if c == nil || c.manager == nil || c.session == nil {
		return Snapshot{}, ErrNotFound
	}
	return c.manager.Get(c.session.owner, c.session.id)
}

func (c *Control) Dispatch(ctx context.Context, event InputEvent) error {
	if c == nil || c.manager == nil || c.browser == nil || c.session == nil {
		return ErrNotReady
	}
	if err := validateInput(event); err != nil {
		return err
	}
	c.manager.mu.Lock()
	if c.manager.active != c.session || !sessionLive(c.session.state) || c.session.browser != c.browser {
		c.manager.mu.Unlock()
		return ErrNotReady
	}
	c.manager.touchLocked(c.session)
	c.manager.mu.Unlock()
	if err := c.browser.Dispatch(ctx, event); err != nil {
		return ErrBrowserProtocol
	}
	return nil
}

func (c *Control) Resize(ctx context.Context, viewport Viewport) error {
	if c == nil || c.manager == nil || c.browser == nil || c.session == nil || !validViewport(viewport) {
		return ErrInputRejected
	}
	if err := c.browser.Resize(ctx, viewport); err != nil {
		return ErrBrowserProtocol
	}
	c.manager.mu.Lock()
	if c.manager.active == c.session && sessionLive(c.session.state) {
		c.session.viewport = viewport
		c.manager.touchLocked(c.session)
	}
	c.manager.mu.Unlock()
	return nil
}

func (c *Control) Verify() (Snapshot, error) {
	if c == nil || c.manager == nil || c.session == nil {
		return Snapshot{}, ErrNotFound
	}
	return c.manager.Verify(c.session.owner, c.session.id)
}

func (c *Control) Terminate() (Snapshot, error) {
	if c == nil || c.manager == nil || c.session == nil {
		return Snapshot{}, ErrNotFound
	}
	return c.manager.Terminate(c.session.owner, c.session.id)
}

// Close releases the control lease but leaves the browser alive until explicit
// termination or session expiry.
func (c *Control) Close() {
	if c == nil || c.manager == nil || c.session == nil {
		return
	}
	c.once.Do(func() {
		c.manager.mu.Lock()
		if c.manager.active == c.session {
			c.session.control = false
		}
		c.manager.mu.Unlock()
	})
}

// HasPersistentProfile avoids starting Chromium merely to inspect a profile
// that has never been initialized.
func (m *Manager) HasPersistentProfile() bool {
	if m == nil {
		return false
	}
	for _, name := range []string{
		"Local State",
		filepath.Join("Default", "Preferences"),
		filepath.Join("Default", "Cookies"),
	} {
		if st, err := os.Stat(filepath.Join(m.opt.ProfileDir, name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// RefreshOnce reopens an existing managed profile without screencast, exports
// cookies, and closes Chromium. It is used at startup and by the periodic loop.
func (m *Manager) RefreshOnce(ctx context.Context) (VerifyResult, error) {
	if m == nil || !m.managedRefreshEnabled() {
		return VerifyResult{}, ErrDisabled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !m.HasPersistentProfile() {
		return VerifyResult{}, ErrNotReady
	}
	m.mu.Lock()
	if m.active != nil && sessionLive(m.active.state) {
		m.mu.Unlock()
		return VerifyResult{}, ErrBusy
	}
	m.mu.Unlock()

	m.profileMu.Lock()
	defer m.profileMu.Unlock()
	m.mu.Lock()
	if m.active != nil && sessionLive(m.active.state) {
		m.mu.Unlock()
		return VerifyResult{}, ErrBusy
	}
	m.mu.Unlock()
	release, allowed := m.beginManagedOperation()
	if !allowed {
		return VerifyResult{}, ErrDisabled
	}
	defer release()

	if err := ensureProfileDir(m.opt.ProfileDir); err != nil {
		return VerifyResult{}, ErrSyncFailed
	}
	browser, err := m.launcher.Launch(ctx, LaunchOptions{
		ExecutablePath: m.opt.BrowserPath,
		ProfileDir:     m.opt.ProfileDir,
		Headless:       m.opt.Headless,
		Viewport:       Viewport{Width: defaultWidth, Height: defaultHeight},
		StartURL:       refreshURL,
		Screencast:     false,
	})
	if err != nil || browser == nil {
		return VerifyResult{}, ErrSyncFailed
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = browser.Close(closeCtx)
		cancel()
	}()

	result, err := exportAndCommitUnlocked(ctx, browser, m.opt.StableFile)
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) && m.opt.Source != nil {
			m.opt.Source.SetManagedAuthenticated(false)
		}
		return result, err
	}
	if m.opt.Source != nil {
		m.opt.Source.SetManagedAuthenticated(true)
	}
	return result, nil
}

// RunRefreshLoop periodically refreshes an initialized managed profile.
func (m *Manager) RunRefreshLoop(ctx context.Context) {
	if m == nil || m.opt.RefreshInterval <= 0 || !m.managedRefreshEnabled() {
		return
	}
	if ctx == nil {
		ctx = m.rootCtx
	}
	ticker := time.NewTicker(m.opt.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !m.HasPersistentProfile() {
				continue
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			result, err := m.RefreshOnce(refreshCtx)
			cancel()
			if err == nil {
				m.log("youtube login: refresh updated=%t logged_in=%t cookies=%d score=%d", result.Updated, result.LoggedIn, result.CookieCount, result.QualityScore)
			} else if !errors.Is(err, ErrBusy) && !errors.Is(err, ErrNotReady) && ctx.Err() == nil {
				m.log("youtube login: refresh result=failed")
			}
		}
	}
}

// Disconnect clears browser cookies from the dedicated profile. If this
// process had selected managed as the active source, its stable jar is removed
// so requests do not continue using the disconnected credentials.
func (m *Manager) Disconnect(ctx context.Context, owner string) error {
	if m == nil || !m.managedEnabled() {
		return ErrDisabled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ErrNotFound
	}

	m.mu.Lock()
	active := m.active
	if active != nil && sessionLive(active.state) {
		if !sameOwner(active.owner, owner) {
			m.mu.Unlock()
			return ErrNotFound
		}
		active.state = StateClosed
		active.errorCode = ""
		active.control = false
		active.cancel()
		browser := active.browser
		m.mu.Unlock()
		if browser != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = browser.Close(closeCtx)
			cancel()
		}
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
	} else {
		m.mu.Unlock()
	}

	m.profileMu.Lock()
	release, allowed := m.beginManagedOperation()
	if !allowed {
		m.profileMu.Unlock()
		return ErrDisabled
	}
	wasManaged := m.opt.Source != nil && m.opt.Source.ManagedAuthenticated()
	if m.HasPersistentProfile() {
		browser, err := m.launcher.Launch(ctx, LaunchOptions{
			ExecutablePath: m.opt.BrowserPath,
			ProfileDir:     m.opt.ProfileDir,
			Headless:       m.opt.Headless,
			Viewport:       Viewport{Width: defaultWidth, Height: defaultHeight},
			StartURL:       refreshURL,
			Screencast:     false,
		})
		if err == nil && browser != nil {
			err = browser.ClearCookies(ctx)
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = browser.Close(closeCtx)
			cancel()
		} else if err == nil {
			err = ErrBrowserClosed
		}
		if err != nil {
			release()
			m.profileMu.Unlock()
			return ErrSyncFailed
		}
	}
	if m.opt.Source != nil {
		m.opt.Source.SetManagedAuthenticated(false)
	}
	if wasManaged {
		if err := cookies.ClearStableFile(m.opt.StableFile); err != nil {
			release()
			m.profileMu.Unlock()
			return ErrSyncFailed
		}
		if err := cookies.RefreshDropIns(filepath.Dir(m.opt.StableFile), m.opt.StableFile); err != nil {
			release()
			m.profileMu.Unlock()
			return ErrSyncFailed
		}
	}
	release()
	m.profileMu.Unlock()
	return nil
}

// Close terminates any active browser and prevents new sessions.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	s := m.active
	var browser Browser
	var done <-chan struct{}
	if s != nil && sessionLive(s.state) {
		s.state = StateClosed
		s.errorCode = ""
		s.control = false
		s.cancel()
		browser = s.browser
		done = s.done
	}
	m.mu.Unlock()
	if browser != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = browser.Close(ctx)
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(4 * time.Second):
		}
	}
}

func (m *Manager) exportAndCommit(ctx context.Context, browser Browser) (VerifyResult, error) {
	release, allowed := m.beginManagedOperation()
	if !allowed {
		return VerifyResult{}, ErrDisabled
	}
	defer release()
	result, err := exportAndCommitUnlocked(ctx, browser, m.opt.StableFile)
	if m.opt.Source != nil {
		switch {
		case err == nil:
			m.opt.Source.SetManagedAuthenticated(true)
		case errors.Is(err, ErrNotLoggedIn):
			m.opt.Source.SetManagedAuthenticated(false)
		}
	}
	return result, err
}

func (m *Manager) beginManagedOperation() (func(), bool) {
	if m.opt.Source == nil {
		return func() {}, true
	}
	return m.opt.Source.BeginManaged()
}

func (m *Manager) lookupLocked(owner, id string) (*loginSession, error) {
	if m.closed || m.active == nil || strings.TrimSpace(id) == "" || m.active.id != strings.TrimSpace(id) {
		return nil, ErrNotFound
	}
	if !sameOwner(m.active.owner, strings.TrimSpace(owner)) {
		return nil, ErrNotFound
	}
	return m.active, nil
}

func (m *Manager) touchLocked(s *loginSession) {
	if s == nil || !sessionLive(s.state) {
		return
	}
	next := m.opt.Now().UTC().Add(m.opt.SessionTTL)
	if next.After(s.hardExpires) {
		next = s.hardExpires
	}
	if next.After(s.expiresAt) {
		s.expiresAt = next
	}
}

func (m *Manager) syncViewportLocked(s *loginSession) {
	if s == nil || s.browser == nil {
		return
	}
	if viewport := s.browser.Viewport(); validViewport(viewport) {
		s.viewport = viewport
	}
}

func (m *Manager) managedEnabled() bool {
	return m != nil && (m.opt.Source == nil || m.opt.Source.ManagedInteractiveEnabled())
}

func (m *Manager) managedRefreshEnabled() bool {
	return m != nil && (m.opt.Source == nil || m.opt.Source.ManagedRefreshEnabled())
}

func (m *Manager) log(format string, args ...any) {
	if m != nil && m.opt.Logf != nil {
		m.opt.Logf(format, args...)
	}
}

func snapshotLocked(s *loginSession) Snapshot {
	if s == nil {
		return Snapshot{}
	}
	return Snapshot{
		ID:                 s.id,
		State:              s.state,
		Error:              s.errorCode,
		CreatedAt:          s.createdAt.UTC().Format(time.RFC3339),
		ExpiresAt:          s.expiresAt.UTC().Format(time.RFC3339),
		Viewport:           s.viewport,
		ChannelPath:        "/api/admin/youtube-login/sessions/" + s.id + "/channel",
		LoggedIn:           s.loggedIn,
		CookieCount:        s.quality.CookieCount,
		YouTubeGoogleCount: s.quality.YouTubeGoogleCount,
		AuthCookieCount:    s.quality.AuthCookieCount,
		QualityScore:       s.quality.QualityScore,
		Updated:            s.quality.Updated,
	}
}

func sessionLive(state string) bool {
	switch state {
	case StateStarting, StateInteractive, StateVerifying, StateAuthenticated, StateNotLoggedIn, StateSyncFailed:
		return true
	default:
		return false
	}
}

func verifyAllowed(state string) bool {
	switch state {
	case StateInteractive, StateNotLoggedIn, StateSyncFailed:
		return true
	default:
		return false
	}
}

func validViewport(v Viewport) bool {
	return v.Width >= 320 && v.Width <= 2560 && v.Height >= 240 && v.Height <= 1600
}

func validateInput(event InputEvent) error {
	if event.Modifiers < 0 || event.Modifiers > 15 {
		return ErrInputRejected
	}
	switch event.Kind {
	case "mouse":
		if math.IsNaN(event.X) || math.IsNaN(event.Y) || math.IsInf(event.X, 0) || math.IsInf(event.Y, 0) ||
			event.X < 0 || event.X > 1 || event.Y < 0 || event.Y > 1 ||
			math.Abs(event.DeltaX) > 10000 || math.Abs(event.DeltaY) > 10000 ||
			event.Buttons < 0 || event.Buttons > 31 || event.ClickCount < 0 || event.ClickCount > 3 {
			return ErrInputRejected
		}
		switch event.EventType {
		case "mousePressed", "mouseReleased", "mouseMoved", "mouseWheel":
		default:
			return ErrInputRejected
		}
		switch event.Button {
		case "", "none", "left", "middle", "right", "back", "forward":
		default:
			return ErrInputRejected
		}
	case "key":
		switch event.EventType {
		case "keyDown", "keyUp", "rawKeyDown", "char":
		default:
			return ErrInputRejected
		}
		if len(event.Key) > 128 || len(event.Code) > 128 || len(event.Text) > 128 ||
			event.WindowsKey < 0 || event.WindowsKey > 65535 ||
			strings.ContainsRune(event.Key, '\x00') || strings.ContainsRune(event.Code, '\x00') || strings.ContainsRune(event.Text, '\x00') {
			return ErrInputRejected
		}
	case "text":
		if event.Text == "" || len(event.Text) > 4096 || strings.ContainsRune(event.Text, '\x00') {
			return ErrInputRejected
		}
	default:
		return ErrInputRejected
	}
	return nil
}

func ensureProfileDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func unsafeProfileDir(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || filepath.Dir(clean) == clean {
		return true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(clean, filepath.Clean(cwd))
	}
	return clean == filepath.Clean(cwd)
}

func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "yl_" + hex.EncodeToString(raw[:]), nil
}

func shortID(id string) string {
	if len(id) <= 11 {
		return id
	}
	return id[:11]
}

func sameOwner(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
