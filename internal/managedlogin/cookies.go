package managedlogin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

var errStablePreserved = errors.New("managedlogin: stronger stable jar preserved")

func exportAndCommitUnlocked(ctx context.Context, browser Browser, stable string) (VerifyResult, error) {
	var result VerifyResult
	if browser == nil {
		return result, ErrNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	browserCookies, err := browser.ExportCookies(ctx)
	if err != nil {
		return result, ErrBrowserProtocol
	}
	filtered := filterManagedCookies(browserCookies)
	if len(filtered) == 0 {
		return result, ErrNotLoggedIn
	}

	tmp, cleanup, err := writeManagedCookieTemp(stable, filtered)
	if err != nil {
		return result, ErrSyncFailed
	}
	defer cleanup()

	commit, err := cookies.CommitSnapshotIfBetterDetailed(tmp, stable)
	if err != nil {
		return result, ErrSyncFailed
	}
	result = VerifyResult{
		Updated:            commit.Updated,
		LoggedIn:           commit.Candidate.LoggedIn,
		CookieCount:        commit.Candidate.CookieCount,
		YouTubeGoogleCount: commit.Candidate.YouTubeGoogleCookies,
		AuthCookieCount:    commit.Candidate.AuthCookies,
		QualityScore:       commit.Candidate.Score,
	}
	if !commit.Candidate.Valid || !commit.Candidate.LoggedIn {
		return result, ErrNotLoggedIn
	}
	if !commit.Updated {
		return result, errStablePreserved
	}
	return result, nil
}

func filterManagedCookies(in []BrowserCookie) []BrowserCookie {
	out := make([]BrowserCookie, 0, len(in))
	for _, c := range in {
		c.Domain = strings.ToLower(strings.TrimSpace(c.Domain))
		c.Path = strings.TrimSpace(c.Path)
		c.Name = strings.TrimSpace(c.Name)
		if c.Path == "" {
			c.Path = "/"
		}
		if !isYouTubeGoogleDomain(c.Domain) || !validCookieField(c.Domain) ||
			!validCookieField(c.Path) || !validCookieField(c.Name) ||
			!validCookieField(c.Value) || c.Name == "" || c.Value == "" ||
			!strings.HasPrefix(c.Path, "/") {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func writeManagedCookieTemp(stable string, list []BrowserCookie) (string, func(), error) {
	stable = strings.TrimSpace(stable)
	if stable == "" {
		return "", func() {}, errors.New("empty stable jar")
	}
	dir := filepath.Dir(filepath.Clean(stable))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp(dir, ".managed-cookies-*.tmp")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	w := bufio.NewWriter(f)
	if _, err := w.WriteString("# Netscape HTTP Cookie File\n"); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	for _, c := range list {
		domain := c.Domain
		includeSubdomains := strings.HasPrefix(domain, ".")
		if c.HTTPOnly {
			domain = "#HttpOnly_" + domain
		}
		expires := int64(0)
		if !c.Session && c.Expires > 0 && !math.IsNaN(c.Expires) && !math.IsInf(c.Expires, 0) {
			expires = int64(math.Floor(c.Expires))
		}
		line := fmt.Sprintf(
			"%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			domain,
			netscapeBool(includeSubdomains),
			c.Path,
			netscapeBool(c.Secure),
			expires,
			c.Name,
			c.Value,
		)
		if _, err := w.WriteString(line); err != nil {
			_ = f.Close()
			cleanup()
			return "", func() {}, err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func validCookieField(value string) bool {
	return !strings.ContainsAny(value, "\t\r\n\x00")
}

func isYouTubeGoogleDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	return domain == "youtube.com" || strings.HasSuffix(domain, ".youtube.com") ||
		domain == "google.com" || strings.HasSuffix(domain, ".google.com") ||
		domain == "google.cn" || strings.HasSuffix(domain, ".google.cn")
}

func netscapeBool(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}
