package cookies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SnapshotForYtdlp copies src to a temp Netscape file for yt-dlp.
// yt-dlp rewrites --cookies in place; never hand it the stable youtube.txt
// or a real login jar will be wiped to anonymous visitor cookies.
// cleanup removes the temp file (safe to call multiple times).
func SnapshotForYtdlp(src string) (tmp string, cleanup func(), err error) {
	src = strings.TrimSpace(src)
	noop := func() {}
	if src == "" {
		return "", noop, nil
	}
	src = filepath.Clean(src)
	cookieJarMu.RLock()
	defer cookieJarMu.RUnlock()
	if !FileExistsNonEmpty(src) {
		return "", noop, nil
	}
	dir := filepath.Dir(src)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", noop, fmt.Errorf("cookies: mkdir for snapshot: %w", err)
	}
	f, err := os.CreateTemp(dir, ".ytdlp-cookies-*.tmp")
	if err != nil {
		// Fallback to process temp dir if cookies dir is not writable.
		f, err = os.CreateTemp("", "ytdlp-cookies-*.tmp")
		if err != nil {
			return "", noop, fmt.Errorf("cookies: create snapshot: %w", err)
		}
	}
	tmp = f.Name()
	_ = f.Close()
	if err := copyFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", noop, fmt.Errorf("cookies: snapshot copy: %w", err)
	}
	// Restrict perms when possible (best-effort on Windows).
	_ = os.Chmod(tmp, 0o600)
	var cleaned bool
	cleanup = func() {
		if cleaned {
			return
		}
		cleaned = true
		_ = os.Remove(tmp)
	}
	return tmp, cleanup, nil
}

// SnapshotCommitResult reports a non-sensitive quality comparison and whether
// the candidate was installed. Current is the resulting stable jar quality.
type SnapshotCommitResult struct {
	Updated   bool
	Candidate CookieQuality
	Previous  CookieQuality
	Current   CookieQuality
}

// CommitSnapshotIfBetter copies tmp onto stable only when it does not
// degrade an existing login jar to anonymous cookies.
func CommitSnapshotIfBetter(tmp, stable string) error {
	_, err := CommitSnapshotIfBetterDetailed(tmp, stable)
	return err
}

// CommitSnapshotIfBetterDetailed is the result-returning form used by browser
// profile synchronization. The old function remains for existing callers.
func CommitSnapshotIfBetterDetailed(tmp, stable string) (SnapshotCommitResult, error) {
	var result SnapshotCommitResult
	tmp = strings.TrimSpace(tmp)
	stable = strings.TrimSpace(stable)
	if tmp == "" || stable == "" {
		return result, nil
	}
	if !FileExistsNonEmpty(tmp) {
		return result, nil
	}
	cookieJarMu.Lock()
	defer cookieJarMu.Unlock()

	now := time.Now()
	candidate, err := inspectCookieFile(tmp, now)
	if err != nil || !candidate.Valid {
		return result, nil
	}
	result.Candidate = candidate
	allow := true
	if FileExistsNonEmpty(stable) {
		previous, inspectErr := inspectCookieFile(stable, now)
		if inspectErr != nil {
			return result, fmt.Errorf("cookies: inspect stable snapshot: %w", inspectErr)
		}
		result.Previous = previous
		result.Current = previous
		if previous.Valid {
			switch {
			case previous.LoggedIn && !candidate.LoggedIn:
				allow = false
			case !previous.LoggedIn && candidate.LoggedIn:
				allow = true
			case candidate.Score < previous.Score:
				allow = false
			}
		}
	}
	if !allow {
		return result, nil
	}
	if err := copyFile(tmp, stable); err != nil {
		return result, fmt.Errorf("cookies: commit snapshot: %w", err)
	}
	result.Updated = true
	result.Current = candidate
	return result, nil
}
