// Command ytmusic-bridge is a YouTube Music search + download HTTP API for bots.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/cookies"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/httpapi"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
	"github.com/xeltra/ytmusic-bridge/internal/version"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	if err := cfg.EnsureDownloadDir(); err != nil {
		return err
	}

	// cookies/ 目录：云容器挂载文件夹；任意 Netscape txt 丢进去即可。
	if err := cookies.TouchDirForMount(cfg.CookiesDir); err != nil {
		log.Printf("warning: cookies dir: %v", err)
	}
	if resolved, err := cookies.Resolve(cookies.ResolveOptions{
		Dir:  cfg.CookiesDir,
		File: cfg.CookiesFile,
	}); err != nil {
		log.Printf("warning: cookies resolve: %v", err)
	} else {
		cfg.CookiesFile = resolved
		if cookies.FileExistsNonEmpty(resolved) {
			log.Printf("cookies: using %s", resolved)
		} else {
			log.Printf("cookies: dir ready at %s (drop any Netscape .txt to enable)", cfg.CookiesDir)
		}
	}

	// Prefer project bin/yt-dlp when YTDLP_PATH is empty.
	if cfg.YtdlpPath == "" {
		if p, err := localBinYtdlp(); err == nil {
			cfg.YtdlpPath = p
		}
	}
	// Prefer project bin/ffmpeg when FFMPEG_LOCATION is empty.
	if cfg.FFmpegLocation == "" {
		if p, err := localBinFFmpeg(); err == nil {
			cfg.FFmpegLocation = p
		}
	}

	ytClient, err := ytmusic.New(ytmusic.Options{
		Timeout:     cfg.SearchTimeout,
		Proxy:       cfg.Proxy,
		CookiesFile: cfg.CookiesFile,
	})
	if err != nil {
		return err
	}
	searchSvc, err := search.New(ytClient, cfg)
	if err != nil {
		return err
	}
	sess := session.NewStore(session.Options{TTL: cfg.SessionTTL})
	dl, err := download.New(cfg, download.Options{})
	if err != nil {
		return err
	}

	ytdlpVer := ""
	if ver, err := download.YtdlpVersion(context.Background(), cfg.YtdlpPath); err != nil {
		log.Printf("warning: yt-dlp version probe failed: %v", err)
	} else {
		ytdlpVer = ver
		log.Printf("yt-dlp version: %s", ytdlpVer)
	}

	api, err := httpapi.New(httpapi.Options{
		Config:       cfg,
		Searcher:     searchSvc,
		Sessions:     sess,
		Downloader:   dl,
		YtdlpVersion: ytdlpVer,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background cleanup: session TTL + cache TTL/max-total.
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		runCleanupLoop(ctx, cfg, sess, dl)
	}()

	// Cookie 自动保活（环境变量 COOKIES_KEEPALIVE=1 开启）。
	keepaliveDone := make(chan struct{})
	go func() {
		defer close(keepaliveDone)
		if !cfg.CookiesKeepAlive {
			return
		}
		log.Printf("cookies keepalive: enabled every %s", cfg.CookiesKeepAliveEvery)
		ticker := time.NewTicker(cfg.CookiesKeepAliveEvery)
		defer ticker.Stop()
		stable := cfg.CookiesFile
		runOnce := func() {
			// 用户新丢的任意文件名 txt → 提升到稳定 youtube.txt（路径不变，无数据竞争）。
			_ = cookies.RefreshDropIns(cfg.CookiesDir, stable)
			if !cookies.FileExistsNonEmpty(stable) {
				log.Printf("cookies keepalive: waiting for file in %s", cfg.CookiesDir)
				return
			}
			snap, cleanup, err := cookies.SnapshotForYtdlp(stable)
			if err != nil {
				log.Printf("cookies keepalive: snapshot: %v", err)
				return
			}
			if snap == "" {
				log.Printf("cookies keepalive: waiting for file in %s", cfg.CookiesDir)
				return
			}
			if err := cookies.KeepAliveOnce(ctx, cookies.KeepAliveOptions{
				CookiesFile: snap,
				YtdlpPath:   cfg.YtdlpPath,
				Proxy:       cfg.Proxy,
			}); err != nil {
				cleanup()
				log.Printf("cookies keepalive: %v", err)
				return
			}
			if err := cookies.CommitSnapshotIfBetter(snap, stable); err != nil {
				cleanup()
				log.Printf("cookies keepalive: commit: %v", err)
				return
			}
			cleanup()
			log.Printf("cookies keepalive: refreshed %s", stable)
		}
		// 启动后短延迟先跑一次
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			runOnce()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("ytmusic-bridge %s listening on http://%s", version.Version, cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		stop()
		<-cleanupDone
		<-keepaliveDone
		return err
	case <-ctx.Done():
		log.Println("shutdown signal received, draining...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		// Wait for cleanup loop to exit after ctx cancel.
		<-cleanupDone
		<-keepaliveDone
		// Final cleanup pass before exit.
		_ = sess.Cleanup()
		if _, cerr := dl.Cleanup(); cerr != nil {
			log.Printf("final cache cleanup: %v", cerr)
		}
		return err
	}
}

func runCleanupLoop(ctx context.Context, cfg *config.Config, sess *session.Store, dl *download.Downloader) {
	interval := cfg.CleanupInterval
	if interval < time.Second {
		interval = time.Second
	}
	// Run once soon after startup so expired state from previous runs is cleared.
	doCleanup(sess, dl)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			doCleanup(sess, dl)
		}
	}
}

func doCleanup(sess *session.Store, dl *download.Downloader) {
	if sess != nil {
		if n := sess.Cleanup(); n > 0 {
			log.Printf("cleanup: removed %d expired sessions", n)
		}
	}
	if dl == nil {
		return
	}
	stats, err := dl.Cleanup()
	if err != nil {
		log.Printf("cleanup: cache error: %v", err)
		return
	}
	if stats.ExpiredRemoved > 0 || stats.SizeRemoved > 0 {
		log.Printf("cleanup: cache expired=%d size=%d freed=%dB entries=%d total=%dB",
			stats.ExpiredRemoved, stats.SizeRemoved, stats.BytesFreed, stats.Entries, stats.TotalBytes)
	}
}

func localBinYtdlp() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"yt-dlp.exe", "yt-dlp"} {
		p := filepath.Join(wd, "bin", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

func localBinFFmpeg() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
		p := filepath.Join(wd, "bin", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	// Directory form is accepted by yt-dlp --ffmpeg-location.
	dir := filepath.Join(wd, "bin")
	if st, err := os.Stat(filepath.Join(dir, "ffprobe.exe")); err == nil && !st.IsDir() {
		return dir, nil
	}
	if st, err := os.Stat(filepath.Join(dir, "ffprobe")); err == nil && !st.IsDir() {
		return dir, nil
	}
	return "", os.ErrNotExist
}
