package download

import (
	"errors"
	"fmt"
	"strings"
)

// 可预期错误：HTTP 层可 errors.Is / errors.As 映射到状态码。
var (
	// ErrBadRequest 请求参数非法（video_id / format 等）。
	ErrBadRequest = errors.New("download: bad request")
	// ErrNotFound 缓存文件或 token 不存在 / 已过期。
	ErrNotFound = errors.New("download: not found")
	// ErrTooLarge 文件超过 MAX_FILESIZE_MB。
	ErrTooLarge = errors.New("download: file too large")
	// ErrYtdlpMissing 找不到 yt-dlp 可执行文件。
	ErrYtdlpMissing = errors.New("download: yt-dlp missing")
	// ErrExecFailed yt-dlp 进程失败。
	ErrExecFailed = errors.New("download: exec failed")
)

// BadRequestError 携带可读原因。
type BadRequestError struct {
	Reason string
}

func (e *BadRequestError) Error() string {
	if e.Reason == "" {
		return ErrBadRequest.Error()
	}
	return "download: " + e.Reason
}

func (e *BadRequestError) Is(target error) bool {
	return target == ErrBadRequest
}

// NotFoundError 缓存未命中或 token 无效。
type NotFoundError struct {
	Reason string
}

func (e *NotFoundError) Error() string {
	if e.Reason == "" {
		return ErrNotFound.Error()
	}
	return "download: " + e.Reason
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// TooLargeError 体积超限。
type TooLargeError struct {
	Size     int64
	MaxBytes int64
	VideoID  string
	Format   string
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("download: file too large: %d bytes > max %d bytes", e.Size, e.MaxBytes)
}

func (e *TooLargeError) Is(target error) bool {
	return target == ErrTooLarge
}

// YtdlpMissingError 明确提示如何配置路径。
type YtdlpMissingError struct {
	Tried string
}

func (e *YtdlpMissingError) Error() string {
	if e.Tried == "" {
		return "download: yt-dlp not found; set YTDLP_PATH to the yt-dlp executable"
	}
	return fmt.Sprintf("download: yt-dlp not found at %q; set YTDLP_PATH to the yt-dlp executable", e.Tried)
}

func (e *YtdlpMissingError) Is(target error) bool {
	return target == ErrYtdlpMissing
}

// ExecError 包装 yt-dlp 失败输出，便于 bot/日志阅读。
type ExecError struct {
	ExitCode int
	Stderr   string
	Stdout   string
	Cmd      string
}

func (e *ExecError) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = e.Stdout
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > 800 {
		msg = msg[:800] + "..."
	}
	if msg == "" {
		return fmt.Sprintf("download: yt-dlp failed (exit=%d)", e.ExitCode)
	}
	return fmt.Sprintf("download: yt-dlp failed (exit=%d): %s", e.ExitCode, msg)
}

func (e *ExecError) Is(target error) bool {
	return target == ErrExecFailed
}
