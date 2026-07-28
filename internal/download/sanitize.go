package download

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
	tokenRe   = regexp.MustCompile(`^[a-f0-9]{16,64}$`)
)

// ValidateVideoID 只允许标准 YouTube video id 字符集，防路径穿越。
func ValidateVideoID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return &BadRequestError{Reason: "video_id is required"}
	}
	if !videoIDRe.MatchString(id) {
		return &BadRequestError{Reason: "invalid video_id"}
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return &BadRequestError{Reason: "invalid video_id"}
	}
	return nil
}

// NormalizeFormat 收敛到 mp3/m4a/opus。
func NormalizeFormat(format, fallback string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(fallback))
	}
	if format == "" {
		format = "mp3"
	}
	switch format {
	case "mp3", "m4a", "opus", "mp4":
		return format, nil
	default:
		return "", &BadRequestError{Reason: "format must be mp3, m4a, opus, or mp4"}
	}
}

// IsVideoFormat reports whether format is a video container (currently mp4 only).
func IsVideoFormat(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "mp4")
}

// IsAudioFormat reports whether format is an audio extract target.
func IsAudioFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3", "m4a", "opus":
		return true
	default:
		return false
	}
}

// ValidateToken 校验 file token（hex）。
func ValidateToken(token string) error {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" || !tokenRe.MatchString(token) {
		return &BadRequestError{Reason: "invalid token"}
	}
	return nil
}

// SanitizeFilename 清洗展示文件名，去掉路径分隔与控制字符。
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "audio"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteByte('_')
		case unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	// 去掉残留的 ".." 片段，避免展示名里仍带路径穿越痕迹。
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "_")
	}
	out = strings.Trim(out, ". ")
	if out == "" || out == "." || out == ".." {
		return "audio"
	}
	// 防止 Windows 保留名
	upper := strings.ToUpper(out)
	switch upper {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "LPT1", "LPT2", "LPT3":
		return "_" + out
	}
	if len(out) > 120 {
		// 按 rune 截断
		rs := []rune(out)
		if len(rs) > 120 {
			out = string(rs[:120])
		}
	}
	return out
}

// ensureUnderDir 确认 absPath 落在 baseDir 之下。
func ensureUnderDir(baseDir, absPath string) error {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(absPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return &BadRequestError{Reason: "path escapes download dir"}
	}
	return nil
}
