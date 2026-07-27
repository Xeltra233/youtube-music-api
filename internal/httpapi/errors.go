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
		writeError(w, http.StatusGatewayTimeout, "TIMEOUT", "上游超时", nil)
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, 499, "CANCELED", "请求已取消", nil)
		return
	}

	// search
	if errors.Is(err, search.ErrEmptyQuery) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "query 不能为空", nil)
		return
	}
	if errors.Is(err, search.ErrInvalidLimit) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "limit 必须 >= 1", nil)
		return
	}

	// session
	var amb *session.AmbiguousError
	if errors.As(err, &amb) {
		detail := map[string]any{
			"name":       amb.Name,
			"candidates": itemsToAPI(amb.Candidates),
		}
		writeError(w, http.StatusConflict, "AMBIGUOUS_NAME", "歌名匹配到多条结果，请改用序号", detail)
		return
	}
	if errors.Is(err, session.ErrGone) {
		writeError(w, http.StatusGone, "SESSION_EXPIRED", "会话已过期，请重新搜索", nil)
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
		writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件超过大小限制", detail)
		return
	}
	if errors.Is(err, download.ErrYtdlpMissing) {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), nil)
		return
	}
	if errors.Is(err, download.ErrExecFailed) {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "下载失败: "+trimErr(err), nil)
		return
	}
	if errors.Is(err, download.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "文件不存在或已过期", nil)
		return
	}
	if errors.Is(err, download.ErrBadRequest) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", trimErr(err), nil)
		return
	}

	// best-effort upstream detection
	msg := err.Error()
	if strings.Contains(msg, "upstream") || strings.Contains(msg, "ytmusic") {
		writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "上游服务失败", map[string]string{"error": trimErr(err)})
		return
	}

	log.Printf("httpapi: unhandled error: %v", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "内部错误", nil)
}

func humanSessionNotFound(err error) string {
	var nf *session.NotFoundError
	if errors.As(err, &nf) && nf.Reason != "" {
		switch {
		case strings.Contains(nf.Reason, "index"):
			return "序号不存在"
		case strings.Contains(nf.Reason, "name"):
			return "未匹配到该歌名"
		case strings.Contains(nf.Reason, "session"):
			return "会话不存在"
		}
	}
	return "未找到"
}

func humanSessionBadRequest(err error) string {
	var br *session.BadRequestError
	if errors.As(err, &br) && br.Reason != "" {
		return br.Reason
	}
	return "请求参数非法"
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
