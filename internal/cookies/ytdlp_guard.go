package cookies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SnapshotForYtdlp copies src to a temp Netscape file for yt-dlp.
// yt-dlp rewrites --cookies in place; never hand it the stable youtube.txt
// or a real login jar will be wiped to anonymous visitor cookies.
// cleanup removes the temp file (safe to call multiple times).
func SnapshotForYtdlp(src string) (tmp string, cleanup func(), err error) {
	src = strings.TrimSpace(src)
	noop := func() {}
	if src == "" || !FileExistsNonEmpty(src) {
		return "", noop, nil
	}
	src = filepath.Clean(src)
	dir := filepath.Dir(src)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", noop, fmt.Errorf("cookies: mkdir for snapshot: %w", err)
	}
	f, err := os.CreateTemp(dir, ".ytdlp-cookies-*.txt")
	if err != nil {
		// Fallback to process temp dir if cookies dir is not writable.
		f, err = os.CreateTemp("", "ytdlp-cookies-*.txt")
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

// CommitSnapshotIfBetter copies tmp onto stable only when it does not
// degrade an existing login jar to anonymous cookies.
func CommitSnapshotIfBetter(tmp, stable string) error {
	tmp = strings.TrimSpace(tmp)
	stable = strings.TrimSpace(stable)
	if tmp == "" || stable == "" {
		return nil
	}
	if !FileExistsNonEmpty(tmp) {
		return nil
	}
	tmpOK, tmpScore := scoreCookieFile(tmp)
	if !tmpOK {
		return nil
	}
	if FileExistsNonEmpty(stable) {
		stableOK, stableScore := scoreCookieFile(stable)
		if stableOK {
			// Never replace a stronger login jar with a weaker anonymous one.
			if tmpScore < stableScore {
				return nil
			}
			// Same quality: only refresh if tmp still looks logged-in enough,
			// or stable was already anonymous.
			if tmpScore == stableScore && !looksLoggedIn(tmp) && looksLoggedIn(stable) {
				return nil
			}
		}
	}
	if err := copyFile(tmp, stable); err != nil {
		return fmt.Errorf("cookies: commit snapshot: %w", err)
	}
	return nil
}

func looksLoggedIn(path string) bool {
	ok, score := scoreCookieFile(path)
	// LOGIN_INFO / SID family alone scores +5 each; visitor-only jars stay low.
	return ok && score >= 10
}
