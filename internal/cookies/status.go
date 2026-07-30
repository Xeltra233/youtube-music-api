package cookies

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

const (
	CookieSourceNone = "none"
	CookieSourceFile = "file"
	// CookieSourceBrowser means the configured primary source is a browser
	// profile; the stable file may still be serving a preserved fallback jar.
	CookieSourceBrowser = "browser"

	CookieSyncResultNever     = "never"
	CookieSyncResultUpdated   = "updated"
	CookieSyncResultPreserved = "preserved"
	CookieSyncResultFailed    = "failed"
	CookieSyncResultCanceled  = "canceled"

	CookieSyncPhaseStartup  = "startup"
	CookieSyncPhasePeriodic = "periodic"
	CookieSyncPhaseManual   = "manual"

	CookieSyncErrorCanceled             = "canceled"
	CookieSyncErrorTimeout              = "timeout"
	CookieSyncErrorProfileLocked        = "profile_database_locked"
	CookieSyncErrorProfileDecrypt       = "profile_decrypt_failed"
	CookieSyncErrorProfileUnavailable   = "profile_unavailable"
	CookieSyncErrorNotLoggedIn          = "not_logged_in"
	CookieSyncErrorMissingYouTubeCookie = "missing_youtube_cookies"
	CookieSyncErrorInvalidJar           = "invalid_cookie_jar"
	CookieSyncErrorYtdlpUnavailable     = "ytdlp_unavailable"
	CookieSyncErrorTemporaryFile        = "temporary_file_failed"
	CookieSyncErrorCommit               = "commit_failed"
	CookieSyncErrorConfiguration        = "invalid_configuration"
	CookieSyncErrorGeneric              = "sync_failed"
)

// CookieFileStatus contains current stable-jar metadata without cookie values.
type CookieFileStatus struct {
	Present    bool
	SizeBytes  int64
	ModifiedAt time.Time
	Quality    CookieQuality
}

// CookieSyncStatus contains browser synchronization metadata only. It excludes
// the browser spec, profile path, proxy, command output, and cookie values.
type CookieSyncStatus struct {
	BrowserConfigured bool
	InProgress        bool
	LastPhase         string
	LastResult        string
	LastError         string
	LastUpdated       bool
	LastSyncAt        time.Time
	LastSuccessAt     time.Time
}

type cookieSyncState struct {
	browserConfigured bool
	inProgress        bool
	lastPhase         string
	lastResult        string
	lastError         string
	lastUpdated       bool
	lastSyncAt        time.Time
	lastSuccessAt     time.Time
}

// InspectCookieFileStatus reads file metadata and quality under the package jar
// read lock, including the Windows atomic-replacement fallback window.
func InspectCookieFileStatus(path string) (CookieFileStatus, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CookieFileStatus{}, nil
	}
	cookieJarMu.RLock()
	defer cookieJarMu.RUnlock()

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CookieFileStatus{}, nil
		}
		return CookieFileStatus{}, err
	}
	if info.IsDir() || info.Size() == 0 {
		return CookieFileStatus{}, nil
	}
	quality, err := inspectCookieFile(path, time.Now())
	if err != nil {
		return CookieFileStatus{}, err
	}
	return CookieFileStatus{
		Present:    true,
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime().UTC(),
		Quality:    quality,
	}, nil
}

// CookieSyncStatus returns a race-safe, already-sanitized lifecycle snapshot.
func (l *CookieLifecycle) CookieSyncStatus() CookieSyncStatus {
	if l == nil {
		return CookieSyncStatus{LastResult: CookieSyncResultNever}
	}
	l.statusMu.RLock()
	status := CookieSyncStatus{
		BrowserConfigured: l.status.browserConfigured,
		InProgress:        l.status.inProgress,
		LastPhase:         l.status.lastPhase,
		LastResult:        l.status.lastResult,
		LastError:         l.status.lastError,
		LastUpdated:       l.status.lastUpdated,
		LastSyncAt:        l.status.lastSyncAt,
		LastSuccessAt:     l.status.lastSuccessAt,
	}
	l.statusMu.RUnlock()
	return SanitizeCookieSyncStatus(status)
}

func (l *CookieLifecycle) configureStatus(opt CookieLifecycleOptions) {
	l.statusMu.Lock()
	l.status.browserConfigured = strings.TrimSpace(opt.BrowserSpec) != ""
	l.statusMu.Unlock()
}

func (l *CookieLifecycle) beginBrowserSync(phase string) {
	l.statusMu.Lock()
	l.status.inProgress = true
	l.status.lastPhase = normalizeCookieSyncPhase(phase)
	l.statusMu.Unlock()
}

func (l *CookieLifecycle) finishBrowserSync(
	phase string,
	result BrowserSyncResult,
	syncErr error,
	contextErr error,
) {
	now := time.Now().UTC()
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	l.status.inProgress = false
	l.status.lastPhase = normalizeCookieSyncPhase(phase)
	l.status.lastUpdated = false
	l.status.lastSyncAt = now
	if syncErr == nil {
		l.status.lastError = ""
		l.status.lastUpdated = result.Updated
		l.status.lastSuccessAt = now
		if result.Updated {
			l.status.lastResult = CookieSyncResultUpdated
		} else {
			l.status.lastResult = CookieSyncResultPreserved
		}
		return
	}
	if errors.Is(syncErr, context.Canceled) || errors.Is(contextErr, context.Canceled) {
		l.status.lastResult = CookieSyncResultCanceled
		l.status.lastError = CookieSyncErrorCanceled
		return
	}
	l.status.lastResult = CookieSyncResultFailed
	l.status.lastError = summarizeBrowserSyncError(syncErr)
}

// SanitizeCookieSyncStatus bounds provider-controlled status values to enums so
// HTTP responses cannot relay arbitrary error text or configuration material.
func SanitizeCookieSyncStatus(status CookieSyncStatus) CookieSyncStatus {
	status.LastPhase = normalizeCookieSyncPhase(status.LastPhase)
	status.LastResult = normalizeCookieSyncResult(status.LastResult)
	status.LastError = normalizeCookieSyncError(status.LastError)
	status.LastSyncAt = status.LastSyncAt.UTC()
	status.LastSuccessAt = status.LastSuccessAt.UTC()
	switch status.LastResult {
	case CookieSyncResultUpdated:
		status.LastError = ""
		status.LastUpdated = true
	case CookieSyncResultPreserved:
		status.LastError = ""
		status.LastUpdated = false
	case CookieSyncResultFailed:
		status.LastUpdated = false
		if status.LastError == "" {
			status.LastError = CookieSyncErrorGeneric
		}
	case CookieSyncResultCanceled:
		status.LastUpdated = false
		status.LastError = CookieSyncErrorCanceled
	case CookieSyncResultNever:
		status.LastError = ""
		status.LastUpdated = false
		status.LastSyncAt = time.Time{}
		status.LastSuccessAt = time.Time{}
	}
	return status
}

func normalizeCookieSyncPhase(phase string) string {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case CookieSyncPhaseStartup:
		return CookieSyncPhaseStartup
	case CookieSyncPhasePeriodic:
		return CookieSyncPhasePeriodic
	case CookieSyncPhaseManual:
		return CookieSyncPhaseManual
	default:
		return ""
	}
}

func normalizeCookieSyncResult(result string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case CookieSyncResultUpdated:
		return CookieSyncResultUpdated
	case CookieSyncResultPreserved:
		return CookieSyncResultPreserved
	case CookieSyncResultFailed:
		return CookieSyncResultFailed
	case CookieSyncResultCanceled:
		return CookieSyncResultCanceled
	case CookieSyncResultNever, "":
		return CookieSyncResultNever
	default:
		return CookieSyncResultFailed
	}
}

func normalizeCookieSyncError(summary string) string {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "":
		return ""
	case CookieSyncErrorCanceled,
		CookieSyncErrorTimeout,
		CookieSyncErrorProfileLocked,
		CookieSyncErrorProfileDecrypt,
		CookieSyncErrorProfileUnavailable,
		CookieSyncErrorNotLoggedIn,
		CookieSyncErrorMissingYouTubeCookie,
		CookieSyncErrorInvalidJar,
		CookieSyncErrorYtdlpUnavailable,
		CookieSyncErrorTemporaryFile,
		CookieSyncErrorCommit,
		CookieSyncErrorConfiguration,
		CookieSyncErrorGeneric:
		return strings.ToLower(strings.TrimSpace(summary))
	default:
		return CookieSyncErrorGeneric
	}
}

func summarizeBrowserSyncError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CookieSyncErrorTimeout
	}
	if errors.Is(err, context.Canceled) {
		return CookieSyncErrorCanceled
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "database is locked"):
		return CookieSyncErrorProfileLocked
	case strings.Contains(message, "could not be decrypted"):
		return CookieSyncErrorProfileDecrypt
	case strings.Contains(message, "profile is unavailable"):
		return CookieSyncErrorProfileUnavailable
	case strings.Contains(message, "not logged in"):
		return CookieSyncErrorNotLoggedIn
	case strings.Contains(message, "no youtube or google cookies"):
		return CookieSyncErrorMissingYouTubeCookie
	case strings.Contains(message, "invalid jar"):
		return CookieSyncErrorInvalidJar
	case strings.Contains(message, "yt-dlp executable"):
		return CookieSyncErrorYtdlpUnavailable
	case strings.Contains(message, "prepare browser cookie jar"):
		return CookieSyncErrorTemporaryFile
	case strings.Contains(message, "commit synchronized jar"):
		return CookieSyncErrorCommit
	case strings.Contains(message, "source is empty"), strings.Contains(message, "stable jar is empty"):
		return CookieSyncErrorConfiguration
	default:
		return CookieSyncErrorGeneric
	}
}
