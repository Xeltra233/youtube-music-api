// Package managedlogin owns the service-managed Chromium profile used by the
// admin frontend's interactive YouTube login flow.
package managedlogin

import (
	"context"
	"errors"
	"time"
)

const (
	StateStarting      = "starting"
	StateInteractive   = "interactive"
	StateVerifying     = "verifying"
	StateAuthenticated = "authenticated"
	StateSynced        = "synced"
	StateNotLoggedIn   = "not_logged_in"
	StateSyncFailed    = "sync_failed"
	StateExpired       = "expired"
	StateClosed        = "closed"
)

const (
	ErrorBrowserUnavailable = "browser_unavailable"
	ErrorBrowserStart       = "browser_start_failed"
	ErrorBrowserClosed      = "browser_closed"
	ErrorSessionExpired     = "session_expired"
	ErrorNotLoggedIn        = "not_logged_in"
	ErrorCookieExport       = "cookie_export_failed"
	ErrorCookieCommit       = "cookie_commit_failed"
	ErrorStablePreserved    = "stable_jar_preserved"
	ErrorInputRejected      = "input_rejected"
)

var (
	ErrDisabled        = errors.New("managed login disabled")
	ErrBusy            = errors.New("managed login session busy")
	ErrNotFound        = errors.New("managed login session not found")
	ErrExpired         = errors.New("managed login session expired")
	ErrNotReady        = errors.New("managed login browser not ready")
	ErrControlBusy     = errors.New("managed login control channel busy")
	ErrVerifyBusy      = errors.New("managed login verification busy")
	ErrNotLoggedIn     = errors.New("managed profile is not logged in")
	ErrSyncFailed      = errors.New("managed cookie synchronization failed")
	ErrInputRejected   = errors.New("managed browser input rejected")
	ErrBrowserClosed   = errors.New("managed browser closed")
	ErrBrowserProtocol = errors.New("managed browser protocol failed")
)

// Viewport is the CSS-pixel browser surface mirrored to the admin frontend.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Snapshot is the complete non-sensitive API representation of one login
// session. It never contains profile paths, browser arguments, cookie values,
// input, or raw browser/protocol errors.
type Snapshot struct {
	ID                 string   `json:"id"`
	State              string   `json:"state"`
	Error              string   `json:"error,omitempty"`
	CreatedAt          string   `json:"created_at"`
	ExpiresAt          string   `json:"expires_at"`
	Viewport           Viewport `json:"viewport"`
	ChannelPath        string   `json:"channel_path"`
	LoggedIn           bool     `json:"logged_in"`
	CookieCount        int      `json:"cookie_count"`
	YouTubeGoogleCount int      `json:"youtube_google_count"`
	AuthCookieCount    int      `json:"auth_cookie_count"`
	QualityScore       int      `json:"quality_score"`
	Updated            bool     `json:"updated"`
}

// Frame is the newest in-memory screencast image. Data is never persisted.
type Frame struct {
	Data     []byte
	Viewport Viewport
}

// BrowserCookie mirrors the subset of CDP Storage.Cookie required for a
// Netscape jar. Values stay in memory until the unique temporary jar is removed.
type BrowserCookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Expires  float64
	HTTPOnly bool
	Secure   bool
	Session  bool
}

// InputEvent is a validated frontend-to-browser action. X/Y are normalized to
// [0,1] and are converted to CSS pixels by the browser controller.
type InputEvent struct {
	Kind       string
	EventType  string
	X          float64
	Y          float64
	Button     string
	Buttons    int
	ClickCount int
	DeltaX     float64
	DeltaY     float64
	Modifiers  int
	Key        string
	Code       string
	Text       string
	WindowsKey int
}

// Browser abstracts one isolated Chromium process for deterministic manager
// tests and the production CDP implementation.
type Browser interface {
	Frames() <-chan Frame
	Done() <-chan error
	Viewport() Viewport
	Dispatch(context.Context, InputEvent) error
	Resize(context.Context, Viewport) error
	ExportCookies(context.Context) ([]BrowserCookie, error)
	ClearCookies(context.Context) error
	Close(context.Context) error
}

// LaunchOptions contains process configuration but is never exposed via HTTP.
type LaunchOptions struct {
	ExecutablePath string
	ProfileDir     string
	Headless       bool
	Viewport       Viewport
	StartURL       string
	Screencast     bool
}

// Launcher starts one browser using the dedicated persistent profile.
type Launcher interface {
	Launch(context.Context, LaunchOptions) (Browser, error)
}

// Options configures Manager.
type Options struct {
	Context         context.Context
	BrowserPath     string
	ProfileDir      string
	Headless        bool
	SessionTTL      time.Duration
	RefreshInterval time.Duration
	StableFile      string
	Launcher        Launcher
	Now             func() time.Time
	Logf            func(format string, args ...any)
	Source          SourceCoordinator
}

// SourceCoordinator is the narrow cookies.SourceArbiter contract used here.
type SourceCoordinator interface {
	ManagedInteractiveEnabled() bool
	ManagedRefreshEnabled() bool
	ManagedAuthenticated() bool
	SetManagedAuthenticated(bool)
	BeginManaged() (release func(), allowed bool)
}

// VerifyResult contains only quality metadata.
type VerifyResult struct {
	Updated            bool
	LoggedIn           bool
	CookieCount        int
	YouTubeGoogleCount int
	AuthCookieCount    int
	QualityScore       int
}
