package cookies

import (
	"strings"
	"sync"
)

const (
	CookieSourceModeAuto     = "auto"
	CookieSourceModeManaged  = "managed"
	CookieSourceModeExternal = "external"
	CookieSourceModeFile     = "file"

	CookieSourceManaged  = "managed"
	CookieSourceExternal = "browser"
)

// SourceArbiter selects exactly one active browser refresh source and also
// serializes every operation that can replace the stable cookie jar.
//
// The external profile and the service-managed profile are alternatives, not
// sequential stages. In auto mode an authenticated managed profile wins;
// otherwise a configured external profile is selected before the file fallback.
type SourceArbiter struct {
	operationMu sync.Mutex
	mu          sync.RWMutex
	mode        string
	external    bool
	managed     bool
}

// NewSourceArbiter creates a source coordinator. Invalid modes are normalized
// to auto so callers that construct Config directly in tests stay predictable.
func NewSourceArbiter(mode string, externalConfigured bool) *SourceArbiter {
	mode = normalizeSourceMode(mode)
	return &SourceArbiter{mode: mode, external: externalConfigured}
}

func normalizeSourceMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case CookieSourceModeManaged:
		return CookieSourceModeManaged
	case CookieSourceModeExternal:
		return CookieSourceModeExternal
	case CookieSourceModeFile:
		return CookieSourceModeFile
	default:
		return CookieSourceModeAuto
	}
}

// Mode returns the normalized configured source mode.
func (a *SourceArbiter) Mode() string {
	if a == nil {
		return CookieSourceModeAuto
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode
}

// ExternalConfigured reports whether COOKIES_FROM_BROWSER is configured.
func (a *SourceArbiter) ExternalConfigured() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.external
}

// ManagedAuthenticated records whether the dedicated managed profile has
// produced an authenticated jar during this process lifetime.
func (a *SourceArbiter) ManagedAuthenticated() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.managed
}

// SetManagedAuthenticated updates managed-profile availability. Callers only
// set true after a verified CDP export has been installed into the stable jar.
func (a *SourceArbiter) SetManagedAuthenticated(authenticated bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.managed = authenticated
	a.mu.Unlock()
}

// ManagedInteractiveEnabled reports whether the configured mode permits the
// admin UI to create a managed browser login session.
func (a *SourceArbiter) ManagedInteractiveEnabled() bool {
	if a == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode == CookieSourceModeAuto || a.mode == CookieSourceModeManaged
}

// ManagedRefreshEnabled reports whether automatic managed-profile probes are
// enabled for the configured mode.
func (a *SourceArbiter) ManagedRefreshEnabled() bool {
	return a.ManagedInteractiveEnabled()
}

// AllowExternalSync reports whether an external profile extraction may start.
func (a *SourceArbiter) AllowExternalSync() bool {
	if a == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	switch a.mode {
	case CookieSourceModeExternal:
		return a.external
	case CookieSourceModeAuto:
		return a.external && !a.managed
	default:
		return false
	}
}

// SelectedSource returns the configured active source without exposing paths
// or cookie values. filePresent only affects the final fallback label.
func (a *SourceArbiter) SelectedSource(filePresent bool) string {
	if a == nil {
		if filePresent {
			return CookieSourceFile
		}
		return CookieSourceNone
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	switch a.mode {
	case CookieSourceModeManaged:
		if a.managed {
			return CookieSourceManaged
		}
	case CookieSourceModeExternal:
		if a.external {
			return CookieSourceExternal
		}
	case CookieSourceModeAuto:
		if a.managed {
			return CookieSourceManaged
		}
		if a.external {
			return CookieSourceExternal
		}
	case CookieSourceModeFile:
		// File fallback below.
	}
	if filePresent {
		return CookieSourceFile
	}
	return CookieSourceNone
}

// BeginExternal serializes a prospective external extraction and rechecks the
// source decision after acquiring the operation lock. The returned release
// function must be called when allowed is true.
func (a *SourceArbiter) BeginExternal() (release func(), allowed bool) {
	if a == nil {
		return func() {}, true
	}
	a.operationMu.Lock()
	if !a.AllowExternalSync() {
		a.operationMu.Unlock()
		return func() {}, false
	}
	return a.operationMu.Unlock, true
}

// BeginManaged serializes a managed browser operation and rechecks whether the
// configured mode permits it.
func (a *SourceArbiter) BeginManaged() (release func(), allowed bool) {
	if a == nil {
		return func() {}, true
	}
	a.operationMu.Lock()
	if !a.ManagedInteractiveEnabled() {
		a.operationMu.Unlock()
		return func() {}, false
	}
	return a.operationMu.Unlock, true
}

// LockOperation serializes non-source-specific stable-jar work such as file
// uploads and keepalive snapshots with managed/external extraction.
func (a *SourceArbiter) LockOperation() func() {
	if a == nil {
		return func() {}
	}
	a.operationMu.Lock()
	return a.operationMu.Unlock
}
