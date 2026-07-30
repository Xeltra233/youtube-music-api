package httpapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/xeltra/ytmusic-bridge/internal/cookies"
)

const maxCookieUploadBytes = 2 << 20 // 2 MiB

var cookieUploadSequence atomic.Uint64

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
	stable := filepath.Join(s.cfg.CookiesDir, cookies.StableFileName)
	if strings.EqualFold(name, cookies.StableFileName) {
		// Keep uploads as drop-ins so only the cookies package replaces the
		// stable jar under its process-wide writer lock.
		name = "upload-" + strconv.FormatInt(s.now().UnixNano(), 10) + "-" +
			strconv.FormatUint(cookieUploadSequence.Add(1), 10) + ".txt"
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
	releaseOperation := func() {}
	if s.cookieSource != nil {
		releaseOperation = s.cookieSource.LockOperation()
	}
	defer func() { releaseOperation() }()

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
	if err := os.Chmod(dest, 0o600); err != nil {
		writeAdminErr(w, http.StatusInternalServerError, "Cookie 文件权限设置失败")
		return
	}

	// An authenticated managed profile owns the active stable jar. Keep this
	// upload as a drop-in fallback and promote it after managed disconnect.
	managedActive := s.cookieSource != nil && s.cookieSource.ManagedAuthenticated()
	if !managedActive {
		if err := cookies.RefreshDropIns(s.cfg.CookiesDir, stable); err != nil {
			writeAdminErr(w, http.StatusInternalServerError, "Cookie 文件提升失败")
			return
		}
	}
	releaseOperation()
	releaseOperation = func() {}

	payload := s.cookieStatusPayload()
	payload["ok"] = true
	payload["uploaded_as"] = filepath.Base(dest)
	payload["message"] = "上传成功"
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) cookieStatusPayload() map[string]any {
	releaseOperation := func() {}
	if s.cookieSource != nil {
		releaseOperation = s.cookieSource.LockOperation()
	}
	defer releaseOperation()

	dir, file := s.activeCookiePath()
	managedActive := s.cookieSource != nil && s.cookieSource.ManagedAuthenticated()
	if dir != "" && !managedActive {
		stable := filepath.Join(dir, cookies.StableFileName)
		if err := cookies.RefreshDropIns(dir, stable); err == nil && cookies.FileExistsNonEmpty(stable) {
			file = stable
		}
	}
	keepalive := false
	intervalSec := 0
	if s.cfg != nil {
		keepalive = s.cfg.CookiesKeepAlive
		if s.cfg.CookiesKeepAliveEvery > 0 {
			intervalSec = int(s.cfg.CookiesKeepAliveEvery / time.Second)
		}
	}
	fileStatus, err := cookies.InspectCookieFileStatus(file)
	if err != nil {
		fileStatus = cookies.CookieFileStatus{}
	}

	syncStatus := cookies.CookieSyncStatus{LastResult: cookies.CookieSyncResultNever}
	if s.cfg != nil {
		syncStatus.BrowserConfigured = s.cfg.HasBrowserCookieSource()
	}
	if s.cookieStatus != nil {
		provided := s.cookieStatus.CookieSyncStatus()
		provided.BrowserConfigured = provided.BrowserConfigured || syncStatus.BrowserConfigured
		syncStatus = provided
	}
	syncStatus = cookies.SanitizeCookieSyncStatus(syncStatus)

	source := cookies.CookieSourceNone
	if syncStatus.BrowserConfigured {
		source = cookies.CookieSourceBrowser
	} else if fileStatus.Present {
		source = cookies.CookieSourceFile
	}
	sourceMode := cookies.CookieSourceModeAuto
	managedEnabled := false
	managedAuthenticated := false
	externalConfigured := syncStatus.BrowserConfigured
	if s.cookieSource != nil {
		source = s.cookieSource.SelectedSource(fileStatus.Present)
		sourceMode = s.cookieSource.Mode()
		managedEnabled = s.cookieSource.ManagedInteractiveEnabled()
		managedAuthenticated = s.cookieSource.ManagedAuthenticated()
		externalConfigured = s.cookieSource.ExternalConfigured()
	} else if s.cfg != nil {
		if strings.TrimSpace(s.cfg.CookieSourceMode) != "" {
			sourceMode = s.cfg.CookieSourceMode
		}
		managedEnabled = s.cfg.ManagedCookieSourceEnabled()
	}
	modUnix, modRFC3339 := statusTime(fileStatus.ModifiedAt)
	lastSyncUnix, lastSyncRFC3339 := statusTime(syncStatus.LastSyncAt)
	lastSuccessUnix, lastSuccessRFC3339 := statusTime(syncStatus.LastSuccessAt)
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
		"ok":                    true,
		"active_file":           safeBaseName(file),
		"present":               fileStatus.Present,
		"size_bytes":            fileStatus.SizeBytes,
		"modified_unix":         modUnix,
		"modified_at":           modRFC3339,
		"keepalive":             keepalive,
		"keepalive_interval":    intervalSec,
		"dropin_files":          dropins,
		"source":                source,
		"source_mode":           sourceMode,
		"managed_enabled":       managedEnabled,
		"managed_authenticated": managedAuthenticated,
		"external_configured":   externalConfigured,
		"browser_configured":    syncStatus.BrowserConfigured,
		"valid":                 fileStatus.Quality.Valid,
		"logged_in":             fileStatus.Quality.LoggedIn,
		"quality_score":         fileStatus.Quality.Score,
		"cookie_count":          fileStatus.Quality.CookieCount,
		"youtube_google_count":  fileStatus.Quality.YouTubeGoogleCookies,
		"auth_cookie_count":     fileStatus.Quality.AuthCookies,
		"sync_in_progress":      syncStatus.InProgress,
		"last_sync_phase":       syncStatus.LastPhase,
		"last_sync_result":      syncStatus.LastResult,
		"last_sync_error":       syncStatus.LastError,
		"last_sync_updated":     syncStatus.LastUpdated,
		"last_sync_unix":        lastSyncUnix,
		"last_sync_at":          lastSyncRFC3339,
		"last_success_unix":     lastSuccessUnix,
		"last_success_at":       lastSuccessRFC3339,
		// Never return cookie contents.
	}
}

func (s *Server) activeCookiePath() (dir, file string) {
	if s == nil || s.cfg == nil {
		return "", ""
	}
	dir = strings.TrimSpace(s.cfg.CookiesDir)
	file = strings.TrimSpace(s.cfg.CookiesFile)
	if dir == "" {
		return dir, file
	}
	stable := filepath.Join(dir, cookies.StableFileName)
	if file == "" || cookies.FileExistsNonEmpty(stable) {
		file = stable
	}
	return dir, file
}

func statusTime(value time.Time) (int64, string) {
	if value.IsZero() {
		return 0, ""
	}
	value = value.UTC()
	return value.Unix(), value.Format(time.RFC3339)
}

func safeBaseName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
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
