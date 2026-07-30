package cookies

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultBrowserCookieProbeURL = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	defaultBrowserSyncTimeout    = 90 * time.Second
)

// BrowserCommandRunner abstracts yt-dlp execution so browser extraction can be
// tested without reading a real browser profile.
type BrowserCommandRunner interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr string, err error)
}

// ExecBrowserCommandRunner executes yt-dlp directly without a shell.
type ExecBrowserCommandRunner struct{}

func (ExecBrowserCommandRunner) Run(ctx context.Context, name string, args []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// BrowserSyncer serializes browser profile reads. Browser cookie databases can
// be locked and their decryption helpers are not safe to fan out concurrently.
type BrowserSyncer struct {
	mu     sync.Mutex
	runner BrowserCommandRunner
}

// NewBrowserSyncer creates a reusable serialized synchronizer. A nil runner
// selects direct os/exec execution.
func NewBrowserSyncer(runner BrowserCommandRunner) *BrowserSyncer {
	return &BrowserSyncer{runner: runner}
}

// BrowserSyncOptions controls one browser-profile-to-Netscape synchronization.
type BrowserSyncOptions struct {
	BrowserSpec string
	StableFile  string
	YtdlpPath   string
	Proxy       string
	ProbeURL    string
	Timeout     time.Duration
}

// BrowserSyncResult contains metadata only; it never includes cookie values,
// profile paths, command output, proxy credentials, or temporary paths.
type BrowserSyncResult struct {
	Updated        bool
	LoggedIn       bool
	CandidateScore int
	StableScore    int
	CookieCount    int
}

// Sync extracts cookies from a browser profile into a temporary jar, validates
// authenticated YouTube/Google cookies, and atomically promotes a non-degrading
// candidate to the stable jar.
func (s *BrowserSyncer) Sync(ctx context.Context, opt BrowserSyncOptions) (BrowserSyncResult, error) {
	var result BrowserSyncResult
	if s == nil {
		return result, errors.New("cookies: browser synchronizer is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, browserContextError(err)
	}

	spec := strings.TrimSpace(opt.BrowserSpec)
	stable := strings.TrimSpace(opt.StableFile)
	if spec == "" {
		return result, errors.New("cookies: browser source is empty")
	}
	if stable == "" {
		return result, errors.New("cookies: stable jar is empty")
	}

	ytdlp, err := resolveBrowserYtdlp(strings.TrimSpace(opt.YtdlpPath))
	if err != nil {
		return result, err
	}
	probeURL := strings.TrimSpace(opt.ProbeURL)
	if probeURL == "" {
		probeURL = defaultBrowserCookieProbeURL
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = defaultBrowserSyncTimeout
	}

	tmp, cleanup, err := createBrowserCookieTemp(stable)
	if err != nil {
		return result, errors.New("cookies: prepare browser cookie jar failed")
	}
	defer cleanup()

	args := []string{
		"--skip-download",
		"--no-playlist",
		"--no-progress",
		"--quiet",
		"--no-warnings",
		"--retries", "2",
		"--socket-timeout", "20",
		"--cookies-from-browser", spec,
		"--cookies", tmp,
		"--", probeURL,
	}
	if proxy := strings.TrimSpace(opt.Proxy); proxy != "" {
		args = append([]string{"--proxy", proxy}, args...)
	}

	runner := s.runner
	if runner == nil {
		runner = ExecBrowserCommandRunner{}
		s.runner = runner
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	stdout, stderr, runErr := runner.Run(runCtx, ytdlp, args)
	runContextErr := runCtx.Err()
	cancel()
	if runContextErr != nil {
		return result, browserContextError(runContextErr)
	}
	if runErr != nil {
		return result, classifyBrowserCommandError(stdout, stderr, runErr)
	}
	_ = os.Chmod(tmp, 0o600)

	candidate, inspectErr := inspectCookieFile(tmp, time.Now())
	if inspectErr != nil || !candidate.Valid {
		return result, errors.New("cookies: browser extraction produced an invalid jar")
	}
	result.CandidateScore = candidate.Score
	result.CookieCount = candidate.CookieCount
	if candidate.YouTubeGoogleCookies == 0 {
		return result, errors.New("cookies: browser extraction has no YouTube or Google cookies")
	}
	if !candidate.LoggedIn {
		return result, errors.New("cookies: browser profile is not logged in")
	}

	commit, commitErr := CommitSnapshotIfBetterDetailed(tmp, stable)
	if commitErr != nil {
		return result, errors.New("cookies: commit synchronized jar failed")
	}
	result.Updated = commit.Updated
	result.LoggedIn = commit.Current.LoggedIn
	result.StableScore = commit.Current.Score
	if !commit.Current.Valid {
		result.LoggedIn = candidate.LoggedIn
		result.StableScore = candidate.Score
	}
	return result, nil
}

func resolveBrowserYtdlp(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if path, err := exec.LookPath("yt-dlp"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("yt-dlp.exe"); err == nil {
		return path, nil
	}
	return "", errors.New("cookies: yt-dlp executable is unavailable")
}

func createBrowserCookieTemp(stable string) (string, func(), error) {
	dir := filepath.Dir(filepath.Clean(stable))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp(dir, ".browser-cookies-*.tmp")
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
	if _, err := f.WriteString("# Netscape HTTP Cookie File\n"); err != nil {
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

func browserContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("cookies: browser profile sync timed out: %w", context.DeadlineExceeded)
	}
	return fmt.Errorf("cookies: browser profile sync canceled: %w", context.Canceled)
}

func classifyBrowserCommandError(stdout, stderr string, runErr error) error {
	var execErr *exec.Error
	var pathErr *os.PathError
	if errors.As(runErr, &execErr) || (errors.As(runErr, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist)) {
		return errors.New("cookies: yt-dlp executable is unavailable")
	}
	message := strings.ToLower(stdout + "\n" + stderr + "\n" + runErr.Error())
	switch {
	case containsAny(message,
		"database is locked",
		"database locked",
		"cookie database is locked",
		"cookies database is locked",
		"could not copy chrome cookie database",
		"could not copy chromium cookie database",
		"could not copy edge cookie database"):
		return errors.New("cookies: browser profile database is locked")
	case containsAny(message,
		"failed to decrypt",
		"could not decrypt",
		"decrypt failed",
		"decryption failed",
		"keyring is not available",
		"failed to get keyring",
		"keyring backend",
		"secretstorage"):
		return errors.New("cookies: browser profile cookies could not be decrypted")
	case containsAny(message,
		"profile does not exist",
		"profile not found",
		"could not find profile",
		"browser profile directory",
		"could not find cookies database",
		"could not find chrome cookies database",
		"could not find chromium cookies database",
		"could not find edge cookies database",
		"could not find firefox cookies database"):
		return errors.New("cookies: browser profile is unavailable")
	case containsAny(message,
		"executable file not found",
		"the system cannot find the file specified"):
		return errors.New("cookies: yt-dlp executable is unavailable")
	default:
		return errors.New("cookies: yt-dlp browser cookie extraction failed")
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
