package httpapi

import (
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// maxJSONBody limits JSON request bodies to reduce DoS risk.
const maxJSONBody = 1 << 20 // 1 MiB

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush keeps http.Flusher support so ServeContent can stream.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// withMiddleware wraps recover + access log + optional API key checks.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 0}

		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v\n%s", rec, debug.Stack())
				if sw.status == 0 {
					writeError(sw, http.StatusInternalServerError, "INTERNAL_ERROR", "内部错误", nil)
				}
			}
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			log.Printf("%s %s %d %dB %s", r.Method, r.URL.Path, status, sw.bytes, time.Since(start).Truncate(time.Millisecond))
		}()

		// optional API key
		if key := strings.TrimSpace(s.apiKey); key != "" {
			got := strings.TrimSpace(r.Header.Get("X-API-Key"))
			if got == "" || got != key {
				writeError(sw, http.StatusUnauthorized, "UNAUTHORIZED", "缺少或错误的 X-API-Key", nil)
				return
			}
		}

		next.ServeHTTP(sw, r)
	})
}

// clientIP extracts the remote host without the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
