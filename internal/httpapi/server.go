package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
	"github.com/xeltra/ytmusic-bridge/internal/version"
)

// Searcher is the search dependency (real service or test stub).
type Searcher interface {
	Search(ctx context.Context, req search.Request) (*search.Response, error)
}

// SessionStore is the session dependency.
type SessionStore interface {
	Put(query string, results []search.Item) (sessionID string, expiresIn int, err error)
	Select(req session.SelectRequest) (session.Selection, error)
}

// Downloader is the download dependency.
type Downloader interface {
	Download(ctx context.Context, req download.Request) (*download.Result, error)
	LookupToken(token string) (*download.Result, error)
}

// Server is the HTTP API surface.
type Server struct {
	cfg        *config.Config
	searcher   Searcher
	sessions   SessionStore
	downloader Downloader
	apiKey     string
	now        func() time.Time
}

// Options configures a Server.
type Options struct {
	Config     *config.Config
	Searcher   Searcher
	Sessions   SessionStore
	Downloader Downloader
	Now        func() time.Time
}

// New builds an HTTP server. cfg/searcher/sessions/downloader are required.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("httpapi: nil config")
	}
	if opts.Searcher == nil {
		return nil, fmt.Errorf("httpapi: nil searcher")
	}
	if opts.Sessions == nil {
		return nil, fmt.Errorf("httpapi: nil sessions")
	}
	if opts.Downloader == nil {
		return nil, fmt.Errorf("httpapi: nil downloader")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		cfg:        opts.Config,
		searcher:   opts.Searcher,
		sessions:   opts.Sessions,
		downloader: opts.Downloader,
		apiKey:     opts.Config.APIKey,
		now:        now,
	}, nil
}

// Handler returns the middleware-wrapped route handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("POST /download", s.handleDownload)
	mux.HandleFunc("GET /file/{token}", s.handleFile)
	return s.withMiddleware(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       version.Version,
		"default_limit": s.cfg.DefaultLimit,
		"max_limit":     s.cfg.MaxLimit,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var body SearchRequestBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	ctx := r.Context()
	if s.cfg.SearchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.SearchTimeout)
		defer cancel()
	}

	resp, err := s.searcher.Search(ctx, search.Request{
		Query:    body.Query,
		Limit:    body.Limit,
		MinScore: body.MinScore,
	})
	if err != nil {
		mapAndWriteError(w, err)
		return
	}

	sid, expiresIn, err := s.sessions.Put(resp.Query, resp.Results)
	if err != nil {
		mapAndWriteError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, SearchResponseBody{
		SessionID:      sid,
		Query:          resp.Query,
		LimitRequested: resp.LimitRequested,
		LimitUsed:      resp.LimitUsed,
		MinScoreUsed:   resp.MinScoreUsed,
		Total:          resp.Total,
		Truncated:      resp.Truncated,
		ExpiresIn:      expiresIn,
		Results:        itemsToAPI(resp.Results),
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var body DownloadRequestBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	sel, err := s.sessions.Select(session.SelectRequest{
		SessionID: body.SessionID,
		Index:     body.Index,
		Name:      body.Name,
		VideoID:   body.VideoID,
	})
	if err != nil {
		mapAndWriteError(w, err)
		return
	}

	ctx := r.Context()
	if s.cfg.DownloadTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.DownloadTimeout)
		defer cancel()
	}

	res, err := s.downloader.Download(ctx, download.Request{
		VideoID:         sel.Item.VideoID,
		Format:          body.Format,
		Title:           sel.Item.Title,
		Artists:         sel.Item.Artists,
		DisplayName:     sel.Item.DisplayName,
		DurationSeconds: sel.Item.DurationSeconds,
	})
	if err != nil {
		mapAndWriteError(w, err)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "json" {
		artists := res.Artists
		if artists == nil {
			artists = []string{}
		}
		writeJSON(w, http.StatusOK, DownloadJSONBody{
			Title:           res.Title,
			Artists:         artists,
			DisplayName:     res.DisplayName,
			VideoID:         res.VideoID,
			DurationSeconds: res.DurationSeconds,
			Format:          res.Format,
			Filesize:        res.Size,
			FileURL:         "/file/" + res.Token,
			ExpiresIn:       res.ExpiresIn,
			Cached:          res.Cached,
		})
		return
	}

	// serveAudioFile writes headers/body via ServeContent; only map errors
	// that happen before any response bytes are written.
	if err := s.serveAudioFile(w, r, res); err != nil {
		mapAndWriteError(w, err)
	}
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	// Reject path-like tokens early; real LookupToken also validates hex.
	if err := download.ValidateToken(token); err != nil {
		mapAndWriteError(w, err)
		return
	}
	res, err := s.downloader.LookupToken(token)
	if err != nil {
		mapAndWriteError(w, err)
		return
	}
	if err := s.serveAudioFile(w, r, res); err != nil {
		mapAndWriteError(w, err)
	}
}

func (s *Server) serveAudioFile(w http.ResponseWriter, r *http.Request, res *download.Result) error {
	if res == nil {
		return &download.NotFoundError{Reason: "nil result"}
	}
	// Keep cached files under DownloadDir only.
	if err := ensurePathInDir(s.cfg.DownloadDir, res.Path); err != nil {
		return err
	}
	f, err := os.Open(res.Path)
	if err != nil {
		return &download.NotFoundError{Reason: "cached file missing"}
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	setTrackHeaders(w, res)
	ct := res.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	filename := res.Filename
	if filename == "" {
		filename = res.VideoID + "." + res.Format
	}
	w.Header().Set("Content-Disposition", contentDisposition(filename))

	// ServeContent handles Range / If-Modified-Since.
	http.ServeContent(w, r, filename, st.ModTime(), f)
	return nil
}

func setTrackHeaders(w http.ResponseWriter, res *download.Result) {
	if res.Title != "" {
		w.Header().Set("X-Track-Title", url.QueryEscape(res.Title))
	}
	if len(res.Artists) > 0 {
		w.Header().Set("X-Track-Artists", url.QueryEscape(strings.Join(res.Artists, ", ")))
	}
	if res.VideoID != "" {
		w.Header().Set("X-Track-Video-Id", res.VideoID)
	}
	if res.DurationSeconds > 0 {
		w.Header().Set("X-Track-Duration", strconv.Itoa(res.DurationSeconds))
	}
	if res.Cached {
		w.Header().Set("X-Cache", "hit")
	} else {
		w.Header().Set("X-Cache", "miss")
	}
}

func contentDisposition(filename string) string {
	// RFC 5987 filename*
	escaped := url.PathEscape(filename)
	return "attachment; filename*=UTF-8''" + escaped
}

func decodeJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("empty body")
	}
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, maxJSONBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("empty body")
	}
	if len(data) > maxJSONBody {
		return fmt.Errorf("body too large")
	}
	// Keep decoder lenient on unknown fields so older bots remain compatible.
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func ensurePathInDir(baseDir, path string) error {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return &download.BadRequestError{Reason: "path escapes download dir"}
	}
	return nil
}
