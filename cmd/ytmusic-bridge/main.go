// Command ytmusic-bridge is a YouTube Music search + download HTTP API for bots.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/httpapi"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
	"github.com/xeltra/ytmusic-bridge/internal/version"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("????: %v", err)
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

	ytClient, err := ytmusic.New(ytmusic.Options{
		Timeout: cfg.SearchTimeout,
		Proxy:   cfg.Proxy,
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
	api, err := httpapi.New(httpapi.Options{
		Config:     cfg,
		Searcher:   searchSvc,
		Sessions:   sess,
		Downloader: dl,
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

	errCh := make(chan error, 1)
	go func() {
		log.Printf("ytmusic-bridge %s ?? http://%s", version.Version, cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("?????????????")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
