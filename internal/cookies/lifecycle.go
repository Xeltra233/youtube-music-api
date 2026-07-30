package cookies

import (
	"context"
	"strings"
	"sync"
	"time"
)

const defaultKeepAliveInitialDelay = 3 * time.Second

// BrowserProfileSyncer is implemented by BrowserSyncer and permits lifecycle
// tests to inject a deterministic profile source.
type BrowserProfileSyncer interface {
	Sync(ctx context.Context, opt BrowserSyncOptions) (BrowserSyncResult, error)
}

// KeepAliveRunner refreshes one temporary cookie snapshot.
type KeepAliveRunner func(ctx context.Context, opt KeepAliveOptions) error

// LifecycleLogFunc receives metadata-only lifecycle messages.
type LifecycleLogFunc func(format string, args ...any)

// CookieLifecycle serializes browser extraction and stable-jar keepalive work.
// Keeping both operations behind one mutex prevents an older keepalive snapshot
// from racing a newly extracted browser jar with the same quality score.
type CookieLifecycle struct {
	operationMu sync.Mutex
	statusMu    sync.RWMutex
	status      cookieSyncState
	browser     BrowserProfileSyncer
	keepAlive   KeepAliveRunner
	logf        LifecycleLogFunc
}

// NewCookieLifecycle creates a coordinator. Nil dependencies select production
// implementations; a nil logger discards lifecycle messages.
func NewCookieLifecycle(browser BrowserProfileSyncer, keepAlive KeepAliveRunner, logf LifecycleLogFunc) *CookieLifecycle {
	if browser == nil {
		browser = NewBrowserSyncer(nil)
	}
	if keepAlive == nil {
		keepAlive = KeepAliveOnce
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &CookieLifecycle{
		browser:   browser,
		keepAlive: keepAlive,
		logf:      logf,
	}
}

// CookieLifecycleOptions is an immutable snapshot of cookie scheduling config.
type CookieLifecycleOptions struct {
	CookiesDir            string
	StableFile            string
	YtdlpPath             string
	Proxy                 string
	BrowserSpec           string
	BrowserSyncOnStart    bool
	BrowserSyncEvery      time.Duration
	BrowserSyncTimeout    time.Duration
	KeepAliveEnabled      bool
	KeepAliveEvery        time.Duration
	KeepAliveInitialDelay time.Duration
	KeepAliveURLs         []string
	// SourceArbiter keeps managed/external/file operations serialized and
	// suppresses external sync while an authenticated managed profile is active.
	SourceArbiter *SourceArbiter
}

// RunStartup attempts one browser extraction before the HTTP server starts.
// Failures are reported and absorbed so an existing stable jar or anonymous
// operation remains available.
func (l *CookieLifecycle) RunStartup(ctx context.Context, opt CookieLifecycleOptions) {
	if l == nil {
		return
	}
	l.configureStatus(opt)
	if !opt.BrowserSyncOnStart || strings.TrimSpace(opt.BrowserSpec) == "" {
		return
	}
	l.runBrowserSync(ctx, opt, "startup")
}

// Run executes periodic browser synchronization and keepalive until ctx is
// canceled. Each tick is handled synchronously, so repeated ticks never fan out
// external yt-dlp processes.
func (l *CookieLifecycle) Run(ctx context.Context, opt CookieLifecycleOptions) {
	if l == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.configureStatus(opt)

	var browserTicker *time.Ticker
	var browserTicks <-chan time.Time
	if strings.TrimSpace(opt.BrowserSpec) != "" && opt.BrowserSyncEvery > 0 {
		browserTicker = time.NewTicker(opt.BrowserSyncEvery)
		browserTicks = browserTicker.C
		defer browserTicker.Stop()
		l.log("cookies browser sync: enabled every %s", opt.BrowserSyncEvery)
	}

	var keepAliveTimer *time.Timer
	var keepAliveTicks <-chan time.Time
	var resetKeepAlive func()
	if opt.KeepAliveEnabled && strings.TrimSpace(opt.StableFile) != "" && opt.KeepAliveEvery > 0 {
		delay := opt.KeepAliveInitialDelay
		if delay <= 0 {
			delay = defaultKeepAliveInitialDelay
		}
		keepAliveTimer = time.NewTimer(delay)
		keepAliveTicks = keepAliveTimer.C
		resetKeepAlive = func() { keepAliveTimer.Reset(opt.KeepAliveEvery) }
		defer keepAliveTimer.Stop()
		l.log("cookies keepalive: enabled every %s", opt.KeepAliveEvery)
	}

	l.runLoop(ctx, opt, browserTicks, keepAliveTicks, resetKeepAlive)
}

func (l *CookieLifecycle) runLoop(
	ctx context.Context,
	opt CookieLifecycleOptions,
	browserTicks <-chan time.Time,
	keepAliveTicks <-chan time.Time,
	resetKeepAlive func(),
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if browserTicks == nil && keepAliveTicks == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-browserTicks:
			if !ok {
				browserTicks = nil
				if keepAliveTicks == nil {
					return
				}
				continue
			}
			l.runBrowserSync(ctx, opt, "periodic")
		case _, ok := <-keepAliveTicks:
			if !ok {
				keepAliveTicks = nil
				if browserTicks == nil {
					return
				}
				continue
			}
			l.runKeepAlive(ctx, opt)
			if resetKeepAlive != nil && ctx.Err() == nil {
				resetKeepAlive()
			}
		}
	}
}

func (l *CookieLifecycle) runBrowserSync(ctx context.Context, opt CookieLifecycleOptions, phase string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.SourceArbiter != nil {
		release, allowed := opt.SourceArbiter.BeginExternal()
		if !allowed {
			return
		}
		defer release()
	}
	l.operationMu.Lock()
	defer l.operationMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if l.browser == nil {
		l.browser = NewBrowserSyncer(nil)
	}
	l.beginBrowserSync(phase)
	result, err := l.browser.Sync(ctx, BrowserSyncOptions{
		BrowserSpec: opt.BrowserSpec,
		StableFile:  opt.StableFile,
		YtdlpPath:   opt.YtdlpPath,
		Proxy:       opt.Proxy,
		Timeout:     opt.BrowserSyncTimeout,
	})
	l.finishBrowserSync(phase, result, err, ctx.Err())
	if err != nil {
		if ctx.Err() == nil {
			l.log("cookies browser sync: %s failed: %v", phase, err)
		}
		return
	}
	l.log(
		"cookies browser sync: %s updated=%t logged_in=%t cookies=%d candidate_score=%d stable_score=%d",
		phase,
		result.Updated,
		result.LoggedIn,
		result.CookieCount,
		result.CandidateScore,
		result.StableScore,
	)
}

func (l *CookieLifecycle) runKeepAlive(ctx context.Context, opt CookieLifecycleOptions) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.SourceArbiter != nil {
		release := opt.SourceArbiter.LockOperation()
		defer release()
	}
	l.operationMu.Lock()
	defer l.operationMu.Unlock()
	if ctx.Err() != nil {
		return
	}

	stable := strings.TrimSpace(opt.StableFile)
	if stable == "" {
		return
	}
	if dir := strings.TrimSpace(opt.CookiesDir); dir != "" {
		if err := RefreshDropIns(dir, stable); err != nil {
			l.log("cookies keepalive: refresh drop-ins failed: %v", err)
			return
		}
	}
	if !FileExistsNonEmpty(stable) {
		l.log("cookies keepalive: waiting for stable jar")
		return
	}
	snapshot, cleanup, err := SnapshotForYtdlp(stable)
	if err != nil {
		l.log("cookies keepalive: snapshot failed: %v", err)
		return
	}
	if snapshot == "" {
		l.log("cookies keepalive: waiting for stable jar")
		return
	}
	defer cleanup()

	if l.keepAlive == nil {
		l.keepAlive = KeepAliveOnce
	}
	err = l.keepAlive(ctx, KeepAliveOptions{
		CookiesFile: snapshot,
		YtdlpPath:   opt.YtdlpPath,
		Proxy:       opt.Proxy,
		URLs:        append([]string(nil), opt.KeepAliveURLs...),
	})
	if err != nil {
		if ctx.Err() == nil {
			l.log("cookies keepalive: refresh failed: %v", err)
		}
		return
	}
	commit, err := CommitSnapshotIfBetterDetailed(snapshot, stable)
	if err != nil {
		l.log("cookies keepalive: commit failed: %v", err)
		return
	}
	if !commit.Updated {
		l.log("cookies keepalive: preserved stronger stable jar")
		return
	}
	l.log("cookies keepalive: refreshed stable jar")
}

func (l *CookieLifecycle) log(format string, args ...any) {
	if l != nil && l.logf != nil {
		l.logf(format, args...)
	}
}
