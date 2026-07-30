// Package cookies 负责 cookies 目录发现、稳定落盘与 Netscape 解析。
// 云容器只挂载文件夹：用户把任意导出的 .txt 丢进目录即可。
package cookies

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StableFileName 是目录内统一使用的稳定文件名，便于保活回写。
const StableFileName = "youtube.txt"

// cookieJarMu coordinates stable jar readers with all in-process replacements.
// Atomic rename already prevents partial files on POSIX; this lock also covers
// the Windows replace/rollback sequence, where the destination is briefly moved.
var cookieJarMu sync.RWMutex

// ResolveOptions 控制如何从目录/显式路径得到可用 cookie 文件。
type ResolveOptions struct {
	// Dir 是挂载的 cookies 目录（可空，空则仅看 File）。
	Dir string
	// File 是显式 COOKIES_FILE；可为文件，或指向目录。
	File string
}

// Resolve 返回最终应使用的 Netscape cookie 文件绝对路径。
// 行为：
//  1. 确保目录存在（便于云上先挂空目录再拷文件）
//  2. 扫描目录内 cookie 文件，按修改时间只保留最新一份到 youtube.txt，并删除更早的 drop-in
//  3. 显式 File 若是有效文件则先纳入目录，再走同一套时间去重
func Resolve(opts ResolveOptions) (string, error) {
	dir := strings.TrimSpace(opts.Dir)
	file := strings.TrimSpace(opts.File)

	if file != "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return "", fmt.Errorf("cookies: resolve file: %w", err)
		}
		st, err := os.Stat(abs)
		if err == nil && st.IsDir() {
			dir = abs
			file = ""
		} else if err == nil && !st.IsDir() {
			// 显式文件存在：纳入 Dir 后统一按时间去重。
			if dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", fmt.Errorf("cookies: mkdir %s: %w", dir, err)
				}
				stable := filepath.Join(dir, StableFileName)
				if filepath.Clean(abs) != filepath.Clean(stable) && !isUnderDir(dir, abs) {
					// 目录外文件先复制进目录，再参与时间裁决。
					inDir := filepath.Join(dir, filepath.Base(abs))
					if err := copyFile(abs, inDir); err != nil {
						if cerr := copyFile(abs, stable); cerr != nil {
							return abs, nil
						}
						return stable, nil
					}
					abs = inDir
				}
				if err := Deduplicate(dir, stable); err != nil {
					return "", err
				}
				if FileExistsNonEmpty(stable) {
					return stable, nil
				}
				if FileExistsNonEmpty(abs) {
					return abs, nil
				}
				return stable, nil
			}
			return abs, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("cookies: stat %s: %w", abs, err)
		}
		// 显式路径尚不存在：若像目录名则当 Dir；否则当“期望的稳定文件路径”。
		if strings.HasSuffix(strings.ToLower(abs), ".txt") || strings.Contains(filepath.Base(abs), ".") {
			parent := filepath.Dir(abs)
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return "", fmt.Errorf("cookies: mkdir %s: %w", parent, err)
			}
			if err := Deduplicate(parent, abs); err != nil {
				return "", err
			}
			return abs, nil
		}
		dir = abs
	}

	if dir == "" {
		dir = "cookies"
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("cookies: resolve dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("cookies: mkdir %s: %w", absDir, err)
	}

	stable := filepath.Join(absDir, StableFileName)
	if err := Deduplicate(absDir, stable); err != nil {
		return "", err
	}
	// 即使目录还空，也返回稳定路径，用户稍后拷文件进来即可。
	return stable, nil
}

// cookieCandidate 是目录内一份可用的 cookie 文件。
type cookieCandidate struct {
	path  string
	score int
	mod   time.Time
	size  int64
}

// CookieQuality contains only non-sensitive metadata about one Netscape jar.
type CookieQuality struct {
	Valid                bool
	Score                int
	LoggedIn             bool
	CookieCount          int
	YouTubeGoogleCookies int
	AuthCookies          int
}

func listCookieCandidates(dir string) ([]cookieCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cookies: readdir %s: %w", dir, err)
	}
	var list []cookieCandidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
		if strings.HasSuffix(low, ".tmp") || strings.HasSuffix(low, ".uploading") {
			continue
		}
		if !(strings.HasSuffix(low, ".txt") || strings.HasSuffix(low, ".cookies") || low == "cookies") {
			continue
		}
		p := filepath.Join(dir, name)
		ok, score := scoreCookieFile(p)
		if !ok {
			continue
		}
		info, _ := e.Info()
		mod := time.Time{}
		var size int64
		if info != nil {
			mod = info.ModTime()
			size = info.Size()
		}
		list = append(list, cookieCandidate{path: p, score: score, mod: mod, size: size})
	}
	return list, nil
}

// sortCandidatesByTime 按修改时间降序（更晚优先）；同秒时用 score、路径稳定决胜。
func sortCandidatesByTime(list []cookieCandidate) {
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].mod.Equal(list[j].mod) {
			return list[i].mod.After(list[j].mod)
		}
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].path < list[j].path
	})
}

// Deduplicate 按修改时间只保留最新 cookie：
//  1. 把最新有效文件内容提升到 stablePath（通常 youtube.txt）
//  2. 删除目录内其它更早的 drop-in cookie 文件
//
// 上传与重启（Resolve）共用此逻辑。
func Deduplicate(dir, stablePath string) error {
	dir = strings.TrimSpace(dir)
	stablePath = strings.TrimSpace(stablePath)
	if dir == "" || stablePath == "" {
		return nil
	}
	cookieJarMu.Lock()
	defer cookieJarMu.Unlock()
	return deduplicateLocked(dir, stablePath)
}

func deduplicateLocked(dir, stablePath string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cookies: mkdir %s: %w", dir, err)
	}
	list, err := listCookieCandidates(dir)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	sortCandidatesByTime(list)
	best := list[0]
	stableClean := filepath.Clean(stablePath)

	// 最新内容落到稳定名。
	if filepath.Clean(best.path) != stableClean {
		if err := copyFile(best.path, stablePath); err != nil {
			return fmt.Errorf("cookies: promote latest to stable: %w", err)
		}
	}

	// 只保留 stable：删除其它 drop-in。
	for _, c := range list {
		if filepath.Clean(c.path) == stableClean {
			continue
		}
		if !isUnderDir(dir, c.path) {
			continue
		}
		_ = os.Remove(c.path)
	}
	return nil
}

// promoteBestInDir 兼容旧调用：按时间去重后返回 stable 路径（若有内容）或空。
func promoteBestInDir(dir, stablePath string) (string, error) {
	if err := Deduplicate(dir, stablePath); err != nil {
		return "", err
	}
	if FileExistsNonEmpty(stablePath) {
		return stablePath, nil
	}
	return "", nil
}

func isUnderDir(dir, path string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, 0o600)
}

// writeFileAtomic writes a complete replacement in the destination directory.
// POSIX rename replaces atomically. Windows may reject replacement, so move the
// old file aside first and roll it back if installing the new file fails.
func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dst = filepath.Clean(dst)
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	keepTmp := true
	defer func() {
		if keepTmp {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err == nil {
		keepTmp = false
		return nil
	}

	dstInfo, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("cookies: replace destination: %w", err)
	}
	if dstInfo.IsDir() {
		return errors.New("cookies: replace destination is a directory")
	}
	backupFile, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".bak-*")
	if err != nil {
		return err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backup)
		return err
	}
	_ = os.Remove(backup)
	if err := os.Rename(dst, backup); err != nil {
		return fmt.Errorf("cookies: move old destination aside: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		rollbackErr := os.Rename(backup, dst)
		return errors.Join(
			fmt.Errorf("cookies: install replacement: %w", err),
			func() error {
				if rollbackErr == nil {
					return nil
				}
				return fmt.Errorf("cookies: restore previous destination: %w", rollbackErr)
			}(),
		)
	}
	keepTmp = false
	_ = os.Remove(backup)
	return nil
}

// IsLikelyNetscape 判断文件是否像 Netscape cookies.txt。
func IsLikelyNetscape(path string) (bool, error) {
	ok, _ := scoreCookieFile(path)
	return ok, nil
}

func scoreCookieFile(path string) (bool, int) {
	quality, err := inspectCookieFile(path, time.Now())
	if err != nil {
		return false, 0
	}
	return quality.Valid, quality.Score
}

func inspectCookieFile(path string, now time.Time) (CookieQuality, error) {
	f, err := os.Open(path)
	if err != nil {
		return CookieQuality{}, err
	}
	defer f.Close()

	quality := CookieQuality{}
	hasLoginInfo := false
	hasSIDFamily := false
	hasAPISIDFamily := false
	scanner := bufio.NewScanner(f)
	// 大 cookie 行
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			if strings.Contains(line, "Netscape") || strings.Contains(line, "HTTP Cookie File") {
				quality.Score += 2
			}
			continue
		}
		domain, name, _, ok := parseActiveCookieLine(line, now)
		if !ok {
			continue
		}
		quality.CookieCount++
		isYTGoogle := isYouTubeGoogleDomain(domain)
		if isYTGoogle {
			quality.YouTubeGoogleCookies++
			quality.Score++
		}
		if !isYTGoogle {
			continue
		}
		switch name {
		case "LOGIN_INFO":
			hasLoginInfo = true
			quality.AuthCookies++
			quality.Score += 8
		case "SID", "__Secure-3PSID", "__Secure-1PSID":
			hasSIDFamily = true
			quality.AuthCookies++
			quality.Score += 5
		case "SAPISID", "APISID", "__Secure-3PAPISID", "__Secure-1PAPISID":
			hasAPISIDFamily = true
			quality.AuthCookies++
			quality.Score += 5
		}
	}
	if err := scanner.Err(); err != nil {
		return CookieQuality{}, err
	}
	quality.Valid = quality.CookieCount > 0
	quality.Score += quality.YouTubeGoogleCookies
	quality.LoggedIn = hasLoginInfo || (hasSIDFamily && hasAPISIDFamily)
	return quality, nil
}

func parseActiveCookieLine(line string, now time.Time) (domain, name, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || (strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_")) {
		return "", "", "", false
	}
	parts := strings.SplitN(line, "\t", 7)
	if len(parts) < 7 {
		return "", "", "", false
	}
	if !isNetscapeBool(parts[1]) || !strings.HasPrefix(strings.TrimSpace(parts[2]), "/") || !isNetscapeBool(parts[3]) {
		return "", "", "", false
	}
	expires, err := strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 64)
	if err != nil || (expires > 0 && expires <= now.Unix()) {
		return "", "", "", false
	}
	domain = strings.ToLower(strings.TrimSpace(parts[0]))
	domain = strings.TrimPrefix(domain, "#httponly_")
	name = strings.TrimSpace(parts[5])
	value = parts[6]
	if domain == "" || name == "" || value == "" {
		return "", "", "", false
	}
	return domain, name, value, true
}

func isNetscapeBool(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true") ||
		strings.EqualFold(strings.TrimSpace(value), "false")
}

func isYouTubeGoogleDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	return domain == "youtube.com" || strings.HasSuffix(domain, ".youtube.com") ||
		domain == "google.com" || strings.HasSuffix(domain, ".google.com") ||
		domain == "google.cn" || strings.HasSuffix(domain, ".google.cn")
}

// HeaderFromFile 把 Netscape 文件转成 Cookie 请求头（供搜索 InnerTube 使用）。
// 优先 youtube / google 域；始终附带 SOCS=CAI。
func HeaderFromFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "SOCS=CAI", nil
	}
	cookieJarMu.RLock()
	defer cookieJarMu.RUnlock()
	if st, err := os.Stat(path); err != nil || st.IsDir() || st.Size() == 0 {
		return "SOCS=CAI", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "SOCS=CAI", err
	}
	defer f.Close()

	// name -> value，后写覆盖先写
	values := map[string]string{}
	order := []string{}
	now := time.Now()
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		domain, name, val, ok := parseActiveCookieLine(line, now)
		if !ok {
			continue
		}
		if !isYouTubeGoogleDomain(domain) {
			continue
		}
		if _, ok := values[name]; !ok {
			order = append(order, name)
		}
		values[name] = val
	}
	if len(values) == 0 {
		return "SOCS=CAI", nil
	}
	if _, ok := values["SOCS"]; !ok {
		order = append([]string{"SOCS"}, order...)
		values["SOCS"] = "CAI"
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if v, ok := values[name]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	return strings.Join(parts, "; "), nil
}

// FileExistsNonEmpty 判断路径是否为非空普通文件。
func FileExistsNonEmpty(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

// RefreshDropIns 扫描 dir，按修改时间只保留最新 cookie 到 stablePath。
// 上传与启动共用 Deduplicate。
func RefreshDropIns(dir, stablePath string) error {
	dir = strings.TrimSpace(dir)
	stablePath = strings.TrimSpace(stablePath)
	if dir == "" || stablePath == "" {
		return nil
	}
	return Deduplicate(dir, stablePath)
}
