package httpapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

const maxCookieUploadBytes = 2 << 20 // 2 MiB

func (s *Server) handleAdminCookieStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.cookieStatusPayload())
}

func (s *Server) handleAdminCookieUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	if s.cfg == nil || strings.TrimSpace(s.cfg.CookiesDir) == "" {
		writeAdminErr(w, http.StatusServiceUnavailable, "cookies dir not configured")
		return
	}
	if err := os.MkdirAll(s.cfg.CookiesDir, 0o755); err != nil {
		writeAdminErr(w, http.StatusInternalServerError, "无法创建 cookies 目录")
		return
	}

	// Limit body early.
	r.Body = http.MaxBytesReader(w, r.Body, maxCookieUploadBytes+64*1024)
	if err := r.ParseMultipartForm(maxCookieUploadBytes + 64*1024); err != nil {
		writeAdminErr(w, http.StatusBadRequest, "上传解析失败或文件过大（最大 2MB）")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		// also accept "cookie" field name
		file, hdr, err = r.FormFile("cookie")
	}
	if err != nil {
		writeAdminErr(w, http.StatusBadRequest, "缺少文件字段 file")
		return
	}
	defer file.Close()

	name := sanitizeUploadName(hdr.Filename)
	if name == "" {
		name = "upload.txt"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".txt") && !strings.HasSuffix(strings.ToLower(name), ".cookies") {
		name = name + ".txt"
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCookieUploadBytes+1))
	if err != nil {
		writeAdminErr(w, http.StatusBadRequest, "读取上传内容失败")
		return
	}
	if len(data) == 0 {
		writeAdminErr(w, http.StatusBadRequest, "文件为空")
		return
	}
	if len(data) > maxCookieUploadBytes {
		writeAdminErr(w, http.StatusRequestEntityTooLarge, "文件过大（最大 2MB）")
		return
	}
	if !looksLikeCookieText(data) {
		writeAdminErr(w, http.StatusBadRequest, "不是有效的 Netscape cookies 文本")
		return
	}

	dest := filepath.Join(s.cfg.CookiesDir, name)
	// Ensure dest stays inside cookies dir.
	if err := ensurePathInDir(s.cfg.CookiesDir, dest); err != nil {
		writeAdminErr(w, http.StatusBadRequest, "非法文件名")
		return
	}
	tmp := dest + ".uploading"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		writeAdminErr(w, http.StatusInternalServerError, "写入失败")
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		// fallback
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			writeAdminErr(w, http.StatusInternalServerError, "写入失败")
			return
		}
	}

	// Promote into stable youtube.txt for runtime use.
	stable := filepath.Join(s.cfg.CookiesDir, cookies.StableFileName)
	_ = cookies.RefreshDropIns(s.cfg.CookiesDir, stable)
	if resolved, err := cookies.Resolve(cookies.ResolveOptions{Dir: s.cfg.CookiesDir, File: s.cfg.CookiesFile}); err == nil && resolved != "" {
		s.cfg.CookiesFile = resolved
	} else {
		s.cfg.CookiesFile = stable
	}

	payload := s.cookieStatusPayload()
	payload["ok"] = true
	payload["uploaded_as"] = filepath.Base(dest)
	payload["message"] = "上传成功"
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) cookieStatusPayload() map[string]any {
	dir := ""
	file := ""
	keepalive := false
	intervalSec := 0
	if s.cfg != nil {
		dir = s.cfg.CookiesDir
		file = s.cfg.CookiesFile
		keepalive = s.cfg.CookiesKeepAlive
		if s.cfg.CookiesKeepAliveEvery > 0 {
			intervalSec = int(s.cfg.CookiesKeepAliveEvery / time.Second)
		}
	}
	// Prefer stable path for status.
	stable := ""
	if dir != "" {
		stable = filepath.Join(dir, cookies.StableFileName)
		_ = cookies.RefreshDropIns(dir, stable)
		if resolved, err := cookies.Resolve(cookies.ResolveOptions{Dir: dir, File: file}); err == nil {
			file = resolved
			if s.cfg != nil {
				s.cfg.CookiesFile = resolved
			}
		}
	}
	present := cookies.FileExistsNonEmpty(file)
	var size int64
	var modUnix int64
	var modRFC3339 string
	if present {
		if st, err := os.Stat(file); err == nil {
			size = st.Size()
			modUnix = st.ModTime().Unix()
			modRFC3339 = st.ModTime().UTC().Format(time.RFC3339)
		}
	}
	// Count drop-in txt files (metadata only).
	dropins := 0
	if dir != "" {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				low := strings.ToLower(e.Name())
				if strings.HasSuffix(low, ".txt") || strings.HasSuffix(low, ".cookies") {
					dropins++
				}
			}
		}
	}
	return map[string]any{
		"ok":                 true,
		"cookies_dir":        dir,
		"active_file":        filepath.Base(file),
		"present":            present,
		"size_bytes":         size,
		"modified_unix":      modUnix,
		"modified_at":        modRFC3339,
		"keepalive":          keepalive,
		"keepalive_interval": intervalSec,
		"dropin_files":       dropins,
		// Never return cookie contents.
	}
}

func sanitizeUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '.' || r == '-' || r == '_' || r == ' ':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			// skip odd chars
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ". ")
	if out == "" || out == "." || out == ".." {
		return "upload.txt"
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

func looksLikeCookieText(data []byte) bool {
	// cheap checks: printable text + netscape-ish tabs or Netscape header
	s := string(data)
	if strings.Contains(s, "Netscape") || strings.Contains(s, "HTTP Cookie File") {
		return true
	}
	lines := 0
	good := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines++
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			good++
		}
		if lines >= 20 {
			break
		}
	}
	if good >= 1 {
		return true
	}
	// reject obvious binary
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return false
}
