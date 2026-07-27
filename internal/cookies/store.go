// Package cookies 负责 cookies 目录发现、稳定落盘与 Netscape 解析。
// 云容器只挂载文件夹：用户把任意导出的 .txt 丢进目录即可。
package cookies

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StableFileName 是目录内统一使用的稳定文件名，便于保活回写。
const StableFileName = "youtube.txt"

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
//  2. 若已有 youtube.txt 则用之
//  3. 否则扫描目录内 .txt，挑最像 YouTube 登录态的一份，复制为 youtube.txt
//  4. 显式 File 若是有效文件则优先（仍建议落在 Dir 下）
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
			// 显式文件存在：若配置了 Dir，尽量同步到稳定名，方便以后只挂目录。
			if dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", fmt.Errorf("cookies: mkdir %s: %w", dir, err)
				}
				stable := filepath.Join(dir, StableFileName)
				if filepath.Clean(abs) != filepath.Clean(stable) {
					if err := copyFile(abs, stable); err == nil {
						return stable, nil
					}
				}
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
			// 若 parent 里已有其它 txt，提升为该路径。
			if promoted, err := promoteBestInDir(parent, abs); err == nil && promoted != "" {
				return promoted, nil
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
	if st, err := os.Stat(stable); err == nil && !st.IsDir() && st.Size() > 0 {
		if ok, _ := IsLikelyNetscape(stable); ok {
			return stable, nil
		}
	}

	if promoted, err := promoteBestInDir(absDir, stable); err != nil {
		return "", err
	} else if promoted != "" {
		return promoted, nil
	}

	// 目录还空：返回稳定路径，用户稍后拷文件进来即可（下载前可再 Resolve）。
	return stable, nil
}

func promoteBestInDir(dir, stablePath string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cookies: readdir %s: %w", dir, err)
	}
	type cand struct {
		path  string
		score int
		mod   time.Time
	}
	var list []cand
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
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
		if info != nil {
			mod = info.ModTime()
		}
		list = append(list, cand{path: p, score: score, mod: mod})
	}
	if len(list) == 0 {
		return "", nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].mod.After(list[j].mod)
	})
	best := list[0].path
	if filepath.Clean(best) == filepath.Clean(stablePath) {
		return stablePath, nil
	}
	if err := copyFile(best, stablePath); err != nil {
		// 复制失败则直接用原文件
		return best, nil
	}
	return stablePath, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		// Windows 上目标存在时 rename 可能失败：直接写
		if werr := os.WriteFile(dst, data, 0o600); werr != nil {
			return werr
		}
	}
	return nil
}

// IsLikelyNetscape 判断文件是否像 Netscape cookies.txt。
func IsLikelyNetscape(path string) (bool, error) {
	ok, _ := scoreCookieFile(path)
	return ok, nil
}

func scoreCookieFile(path string) (bool, int) {
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer f.Close()

	score := 0
	lines := 0
	ytLines := 0
	scanner := bufio.NewScanner(f)
	// 大 cookie 行
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if strings.Contains(line, "Netscape") || strings.Contains(line, "HTTP Cookie File") {
				score += 2
			}
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		lines++
		domain := strings.ToLower(parts[0])
		name := parts[5]
		if strings.Contains(domain, "youtube.com") || strings.Contains(domain, "google.com") {
			ytLines++
			score++
		}
		switch name {
		case "LOGIN_INFO", "SID", "__Secure-3PSID", "__Secure-1PSID", "SAPISID", "APISID", "__Secure-3PAPISID", "__Secure-1PAPISID":
			score += 5
		}
	}
	if lines == 0 {
		return false, 0
	}
	if ytLines == 0 && score < 3 {
		// 可能是别的站 cookie，仍算 netscape，但分低
		return true, score
	}
	return true, score + ytLines
}

// HeaderFromFile 把 Netscape 文件转成 Cookie 请求头（供搜索 InnerTube 使用）。
// 优先 youtube / google 域；始终附带 SOCS=CAI。
func HeaderFromFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "SOCS=CAI", nil
	}
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
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		domain := strings.ToLower(parts[0])
		name := parts[5]
		val := parts[6]
		if name == "" || val == "" {
			continue
		}
		if !(strings.Contains(domain, "youtube.com") ||
			strings.Contains(domain, "google.com") ||
			strings.Contains(domain, "google.cn")) {
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

// RefreshDropIns 扫描 dir，把更好的 drop-in 提升到 stablePath（通常是 youtube.txt）。
// 若 stable 已有内容且仍有效，仅在 drop-in 明显更新时覆盖。
func RefreshDropIns(dir, stablePath string) error {
	dir = strings.TrimSpace(dir)
	stablePath = strings.TrimSpace(stablePath)
	if dir == "" || stablePath == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return promoteBestInDirSmart(dir, stablePath)
}

func promoteBestInDirSmart(dir, stablePath string) error {
	stableOK, stableScore := false, 0
	if st, err := os.Stat(stablePath); err == nil && !st.IsDir() && st.Size() > 0 {
		stableOK, stableScore = scoreCookieFile(stablePath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type cand struct {
		path  string
		score int
		mod   time.Time
		size  int64
	}
	var list []cand
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
		if !(strings.HasSuffix(low, ".txt") || strings.HasSuffix(low, ".cookies") || low == "cookies") {
			continue
		}
		p := filepath.Join(dir, name)
		if filepath.Clean(p) == filepath.Clean(stablePath) {
			continue
		}
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
		list = append(list, cand{path: p, score: score, mod: mod, size: size})
	}
	if len(list) == 0 {
		if !stableOK {
			// 尝试走原逻辑（例如只有 stable 自己）
			_, _ = promoteBestInDir(dir, stablePath)
		}
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].mod.After(list[j].mod)
	})
	best := list[0]
	if stableOK && best.score < stableScore {
		return nil
	}
	if stableOK && best.score == stableScore {
		// 同质量时，仅当 drop-in 更新且更大/更新才覆盖
		st, err := os.Stat(stablePath)
		if err == nil && !best.mod.After(st.ModTime()) {
			return nil
		}
	}
	return copyFile(best.path, stablePath)
}
