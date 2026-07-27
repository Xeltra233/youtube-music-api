package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CommandRunner 抽象 exec，便于单测注入 fake yt-dlp。
type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, env []string) (stdout, stderr string, err error)
}

// ExecRunner 是真实 os/exec 实现。
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, env []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// YtdlpOptions 构造 yt-dlp 命令所需参数。
type YtdlpOptions struct {
	YtdlpPath      string
	FFmpegLocation string
	Proxy          string
	CookiesFile    string
	Format         string
	Bitrate        string
	OutputPath     string // 最终期望文件路径（含扩展名）
	URL            string
	MaxFilesize    int64 // 字节；0 表示不限制
}

func resolveYtdlpPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	}
	// 常见名字
	candidates = append(candidates, "yt-dlp", "yt-dlp.exe")

	for _, c := range candidates {
		if c == "yt-dlp" || c == "yt-dlp.exe" {
			if p, err := exec.LookPath(c); err == nil {
				return p, nil
			}
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
		// 也允许配置目录 + 默认文件名
		if st, err := os.Stat(filepath.Join(c, "yt-dlp.exe")); err == nil && !st.IsDir() {
			return filepath.Join(c, "yt-dlp.exe"), nil
		}
		if st, err := os.Stat(filepath.Join(c, "yt-dlp")); err == nil && !st.IsDir() {
			return filepath.Join(c, "yt-dlp"), nil
		}
	}
	return "", &YtdlpMissingError{Tried: configured}
}

func buildYtdlpArgs(opt YtdlpOptions) []string {
	// 输出模板指向最终路径，但我们会先写到临时文件再 rename；
	// 这里 OutputPath 已是 temp 路径。
	args := []string{
		"--no-playlist",
		"--no-progress",
		"--newline",
		"--extract-audio",
		"--audio-format", opt.Format,
		"--audio-quality", opt.Bitrate + "K",
		"--output", opt.OutputPath,
		// 避免写 description 等附属文件
		"--no-write-playlist-metafiles",
		"--no-write-comments",
		"--no-write-info-json",
		"--no-write-thumbnail",
		"--no-mtime",
	}
	if opt.FFmpegLocation != "" {
		args = append(args, "--ffmpeg-location", opt.FFmpegLocation)
	}
	if opt.Proxy != "" {
		args = append(args, "--proxy", opt.Proxy)
	}
	if opt.CookiesFile != "" {
		args = append(args, "--cookies", opt.CookiesFile)
	}
	if opt.MaxFilesize > 0 {
		// yt-dlp 的 max-filesize 在下载阶段生效；转码后还会再检查一次。
		args = append(args, "--max-filesize", fmt.Sprintf("%d", opt.MaxFilesize))
	}
	// 稳定的音频选择：优先 m4a/webm 音频流
	args = append(args, "-f", "bestaudio/best")
	args = append(args, "--", opt.URL)
	return args
}

func runYtdlp(ctx context.Context, runner CommandRunner, opt YtdlpOptions) error {
	if runner == nil {
		runner = ExecRunner{}
	}
	path, err := resolveYtdlpPath(opt.YtdlpPath)
	if err != nil {
		return err
	}
	args := buildYtdlpArgs(opt)
	stdout, stderr, err := runner.Run(ctx, path, args, nil)
	if err == nil {
		return nil
	}
	exitCode := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exitCode = ee.ExitCode()
	}
	// context 取消/超时优先
	if ctx.Err() != nil {
		return fmt.Errorf("download: yt-dlp canceled: %w", ctx.Err())
	}
	return &ExecError{
		ExitCode: exitCode,
		Stderr:   stderr,
		Stdout:   stdout,
		Cmd:      path + " " + strings.Join(args, " "),
	}
}

// findProducedFile 在 temp 输出旁寻找 yt-dlp 实际写出的文件。
// yt-dlp 有时会在扩展名前插入 id 或改扩展名，需宽容匹配。
func findProducedFile(outputPath string, format string) (string, error) {
	// 1) 精确路径
	if st, err := os.Stat(outputPath); err == nil && st.Size() > 0 {
		return outputPath, nil
	}
	dir := filepath.Dir(outputPath)
	base := filepath.Base(outputPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	// 2) 同 stem 任意扩展
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == base || strings.HasPrefix(name, stem) {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				candidates = append(candidates, p)
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	// 3) 优先匹配目标 format 扩展
	ext := "." + format
	for _, p := range candidates {
		if strings.EqualFold(filepath.Ext(p), ext) {
			return p, nil
		}
	}
	if len(candidates) > 0 {
		// 取最新修改
		best := candidates[0]
		var bestT time.Time
		for _, p := range candidates {
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			if st.ModTime().After(bestT) {
				bestT = st.ModTime()
				best = p
			}
		}
		return best, nil
	}
	return "", fmt.Errorf("download: yt-dlp produced no output file near %s", outputPath)
}
