package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/xeltra/ytmusic-bridge/internal/download"
	"github.com/xeltra/ytmusic-bridge/internal/search"
	"github.com/xeltra/ytmusic-bridge/internal/session"
)

// ErrorBody is the unified JSON error envelope for bots.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, detail any) {
	// Always emit detail, including explicit null for bot-side parsers.
	writeJSON(w, status, ErrorBody{Code: code, Message: message, Detail: detail})
}

func mapAndWriteError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	// context timeout / cancel
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "TIMEOUT", "????", nil)
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, 499, "CANCELED", "?????", nil)
		return
	}

	// search
	if errors.Is(err, search.ErrEmptyQuery) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "query ????", nil)
		return
	}
	if errors.Is(err, search.ErrInvalidLimit) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "limit ?? >= 1", nil)
		return
	}

	// session
	var amb *session.AmbiguousError
	if errors.As(err, &amb) {
		detail := map[string]any{
			"name":       amb.Name,
			"candidates": itemsToAPI(amb.Candidates),
		}
		writeError(w, http.StatusConflict, "AMBIGUOUS_NAME", "???????????????", detail)
		return
	}
	if errors.Is(err, session.ErrGone) {
		writeError(w, http.StatusGone, "SESSION_EXPIRED", "???????????", nil)
		return
	}
	if errors.Is(err, session.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", humanSessionNotFound(err), nil)
		return
	}
	if errors.Is(err, session.ErrBadRequest) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", humanSessionBadRequest(err), nil)
		return
	}

	// download
	var tooLarge *download.TooLargeError
	if errors.As(err, &tooLarge) || errors.Is(err, download.ErrTooLarge) {
		detail := any(nil)
		if tooLarge != nil {
			detail = map[string]any{
				"size":      tooLarge.Size,
				"max_bytes": tooLarge.MaxBytes,
				"video_id":  tooLarge.VideoID,
				"format":    tooLarge.Format,
			}
		}
		writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "?????????????", detail)
		return
	}
	if errors.Is(err, download.ErrYtdlpMissing) {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
		return
	}
	if errors.Is(err, download.ErrExecFailed) {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "????: "+trimErr(err), nil)
		return
	}
	if errors.Is(err, download.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "?????????", nil)
		return
	}
	if errors.Is(err, download.ErrBadRequest) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", trimErr(err), nil)
		return
	}

	// best-effort upstream detection
	msg := err.Error()
	if strings.Contains(msg, "upstream") || strings.Contains(msg, "ytmusic") {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "??????", map[string]string{"error": trimErr(err)})
		return
	}

	log.Printf("httpapi: unhandled error: %v", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "???????", nil)
}

func humanSessionNotFound(err error) string {
	var nf *session.NotFoundError
	if errors.As(err, &nf) && nf.Reason != "" {
		switch {
		case strings.Contains(nf.Reason, "index"):
			return "?????"
		case strings.Contains(nf.Reason, "name"):
			return "????????"
		case strings.Contains(nf.Reason, "session"):
			return "?????"
		}
	}
	return "???"
}

func humanSessionBadRequest(err error) string {
	var br *session.BadRequestError
	if errors.As(err, &br) && br.Reason != "" {
		return br.Reason
	}
	return "??????"
}

func trimErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
