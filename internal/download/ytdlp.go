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
	// Project-local bin/ (populated by scripts/get-ytdlp.ps1).
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "bin", "yt-dlp.exe"),
			filepath.Join(wd, "bin", "yt-dlp"),
		)
	}
	// Common names on PATH.
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
		// Also allow configuring a directory + default filename.
		if st, err := os.Stat(filepath.Join(c, "yt-dlp.exe")); err == nil && !st.IsDir() {
			return filepath.Join(c, "yt-dlp.exe"), nil
		}
		if st, err := os.Stat(filepath.Join(c, "yt-dlp")); err == nil && !st.IsDir() {
			return filepath.Join(c, "yt-dlp"), nil
		}
	}
	return "", &YtdlpMissingError{Tried: configured}
}

// YtdlpVersion runs `yt-dlp --version` and returns a trimmed version string.
// path may be empty; resolveYtdlpPath is used as a fallback.
func YtdlpVersion(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resolved := strings.TrimSpace(path)
	var err error
	if resolved == "" {
		resolved, err = resolveYtdlpPath("")
		if err != nil {
			return "", err
		}
	} else if st, serr := os.Stat(resolved); serr != nil || st.IsDir() {
		// Allow directory or unresolved configured path.
		resolved, err = resolveYtdlpPath(path)
		if err != nil {
			return "", err
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	stdout, stderr, err := ExecRunner{}.Run(cctx, resolved, []string{"--version"}, nil)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("download: yt-dlp --version failed: %s", msg)
	}
	ver := strings.TrimSpace(stdout)
	if ver == "" {
		ver = strings.TrimSpace(stderr)
	}
	// yt-dlp prints a single line version like 2026.07.04
	ver = strings.ReplaceAll(ver, "\r\n", "\n")
	ver = strings.ReplaceAll(ver, "\r", "\n")
	if i := strings.IndexByte(ver, '\n'); i >= 0 {
		ver = strings.TrimSpace(ver[:i])
	}
	if ver == "" {
		return "", fmt.Errorf("download: empty yt-dlp version output")
	}
	return ver, nil
}

func buildYtdlpArgs(opt YtdlpOptions) []string {
	return buildYtdlpArgsWithExtra(opt, nil)
}

func buildYtdlpArgsWithExtra(opt YtdlpOptions, extra []string) []string {
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
		// 对齐 spotube YtDlpEngine 的稳定开关
		"--no-check-certificate",
		"--ignore-errors",
		// 下载稳定性：让 yt-dlp 自己先重试分片/瞬时网络错误
		"--retries", "5",
		"--fragment-retries", "5",
		"--retry-sleep", "linear=1::2",
		"--socket-timeout", "20",
		"--geo-bypass",
		"--no-cache-dir",
		"--prefer-ffmpeg",
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
	// 额外策略参数（player_client 等）放在 -f 之前。
	if len(extra) > 0 {
		args = append(args, extra...)
	}
	// spotube / 实测：ba 优先，再回退 bestaudio/best。
	args = append(args, "-f", "ba/bestaudio/best")
	args = append(args, "--", opt.URL)
	return args
}

// buildYtdlpRawArgs 只拉原始音轨，不在 yt-dlp 内转码（更接近 spotube 取流）。
func buildYtdlpRawArgs(opt YtdlpOptions, extra []string) []string {
	args := []string{
		"--no-playlist",
		"--no-progress",
		"--newline",
		"--output", opt.OutputPath,
		"--no-write-playlist-metafiles",
		"--no-write-comments",
		"--no-write-info-json",
		"--no-write-thumbnail",
		"--no-mtime",
		"--no-check-certificate",
		"--ignore-errors",
		"--retries", "5",
		"--fragment-retries", "5",
		"--retry-sleep", "linear=1::2",
		"--socket-timeout", "20",
		"--geo-bypass",
		"--no-cache-dir",
	}
	if opt.FFmpegLocation != "" {
		// raw 模式不强制转码，但保留位置无害。
		args = append(args, "--ffmpeg-location", opt.FFmpegLocation)
	}
	if opt.Proxy != "" {
		args = append(args, "--proxy", opt.Proxy)
	}
	if opt.CookiesFile != "" {
		args = append(args, "--cookies", opt.CookiesFile)
	}
	if opt.MaxFilesize > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%d", opt.MaxFilesize))
	}
	if len(extra) > 0 {
		args = append(args, extra...)
	}
	args = append(args, "-f", "ba/bestaudio/best")
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
	if ctx == nil {
		ctx = context.Background()
	}

	// 策略顺序来自 spotube + 本机实测：
	// 1) android_vr 最稳，不依赖 PO Token / JS runtime
	// 2) 默认（yt-dlp 常自动走 android_vr）
	// 3) 其它客户端兜底
	// 注意：ios/android 常要 GVS PO Token，放最后且可跳过硬失败。
	strategies := []struct {
		name  string
		extra []string
	}{
		{name: "android_vr", extra: []string{"--extractor-args", "youtube:player_client=android_vr"}},
		{name: "default", extra: nil},
		{name: "web_safari", extra: []string{"--extractor-args", "youtube:player_client=web_safari"}},
		{name: "mweb", extra: []string{"--extractor-args", "youtube:player_client=mweb"}},
		{name: "tv_embedded", extra: []string{"--extractor-args", "youtube:player_client=tv_embedded"}},
		{name: "android", extra: []string{"--extractor-args", "youtube:player_client=android"}},
	}

	var last error
	for i, st := range strategies {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("download: yt-dlp canceled: %w", err)
		}
		if i > 0 {
			// 策略间短退避，避免连续打同一风控。
			delay := time.Duration(i) * 400 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("download: yt-dlp canceled: %w", ctx.Err())
			case <-timer.C:
			}
		}

		// 先走 spotube 同类「一键取流+转码」；失败再 raw 下载 + 本地 ffmpeg。
		execErr := runYtdlpAttempt(ctx, runner, path, buildYtdlpArgsWithExtra(opt, st.extra), st.name+"|extract")
		if execErr == nil {
			return nil
		}
		last = execErr
		if !isTransientYtdlpError(execErr) {
			return execErr
		}

		// raw 回退：先下原始音轨，再 ffmpeg 转目标格式（更接近 spotube 只取流）。
		rawErr := runYtdlpRawThenConvert(ctx, runner, path, opt, st.extra, st.name)
		if rawErr == nil {
			return nil
		}
		last = rawErr
		if !isTransientYtdlpError(rawErr) {
			return rawErr
		}
	}
	if last == nil {
		return fmt.Errorf("download: yt-dlp failed with no attempts")
	}
	return last
}

func runYtdlpAttempt(ctx context.Context, runner CommandRunner, path string, args []string, strategy string) error {
	stdout, stderr, err := runner.Run(ctx, path, args, nil)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("download: yt-dlp canceled: %w", ctx.Err())
	}
	exitCode := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exitCode = ee.ExitCode()
	}
	return &ExecError{
		ExitCode: exitCode,
		Stderr:   stderr,
		Stdout:   stdout,
		Cmd:      path + " " + strings.Join(args, " "),
		Strategy: strategy,
	}
}

func runYtdlpRawThenConvert(ctx context.Context, runner CommandRunner, ytdlpPath string, opt YtdlpOptions, extra []string, strategy string) error {
	// 把输出模板改成 raw 旁路，避免覆盖 extract 残留逻辑；仍落在同一目录。
	rawOpt := opt
	// OutputPath 形如 stem.%(ext)s；raw 复用。
	args := buildYtdlpRawArgs(rawOpt, extra)
	if err := runYtdlpAttempt(ctx, runner, ytdlpPath, args, strategy+"|raw"); err != nil {
		return err
	}

	// 找到 raw 文件
	stem := strings.TrimSuffix(opt.OutputPath, ".%(ext)s")
	if stem == opt.OutputPath {
		stem = strings.TrimSuffix(opt.OutputPath, filepath.Ext(opt.OutputPath))
	}
	produced, err := findProducedFile(stem+".webm", opt.Format)
	if err != nil {
		produced, err = findProducedFile(stem+".m4a", opt.Format)
	}
	if err != nil {
		produced, err = findProducedFile(stem+".tmp", opt.Format)
	}
	if err != nil {
		return &ExecError{Stderr: err.Error(), Strategy: strategy + "|raw-missing"}
	}

	// 已是目标格式则直接可用
	if strings.EqualFold(filepath.Ext(produced), "."+opt.Format) {
		return nil
	}

	target := stem + "." + opt.Format
	if err := convertAudioFile(ctx, opt.FFmpegLocation, produced, target, opt.Format, opt.Bitrate); err != nil {
		return &ExecError{Stderr: err.Error(), Strategy: strategy + "|ffmpeg"}
	}
	// 转码成功后尽量删 raw，节省磁盘
	if filepath.Clean(produced) != filepath.Clean(target) {
		_ = os.Remove(produced)
	}
	return nil
}

func convertAudioFile(ctx context.Context, ffmpegLocation, input, output, format, bitrate string) error {
	ffmpeg, err := resolveFFmpegPath(ffmpegLocation)
	if err != nil {
		return err
	}
	args := []string{"-y", "-i", input, "-vn"}
	switch strings.ToLower(format) {
	case "mp3":
		args = append(args, "-acodec", "libmp3lame", "-b:a", bitrate+"K")
	case "m4a":
		args = append(args, "-acodec", "aac", "-b:a", bitrate+"K")
	case "opus":
		args = append(args, "-acodec", "libopus", "-b:a", bitrate+"K")
	default:
		return fmt.Errorf("unsupported convert format %q", format)
	}
	args = append(args, output)

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg convert failed: %s", msg)
	}
	if st, err := os.Stat(output); err != nil || st.Size() <= 0 {
		return fmt.Errorf("ffmpeg produced empty output")
	}
	return nil
}

func resolveFFmpegPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
		// 若配置的是目录
		candidates = append(candidates,
			filepath.Join(configured, "ffmpeg"),
			filepath.Join(configured, "ffmpeg.exe"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "bin", "ffmpeg"),
			filepath.Join(wd, "bin", "ffmpeg.exe"),
		)
	}
	candidates = append(candidates, "ffmpeg", "ffmpeg.exe")
	for _, c := range candidates {
		if c == "ffmpeg" || c == "ffmpeg.exe" {
			if p, err := exec.LookPath(c); err == nil {
				return p, nil
			}
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found; set FFMPEG_LOCATION")
}

// isTransientYtdlpError 判断是否值得换 player client / 再试一次。
func isTransientYtdlpError(err error) bool {
	if err == nil {
		return false
	}
	var ee *ExecError
	msg := err.Error()
	if errors.As(err, &ee) {
		msg = ee.Stderr + "\n" + ee.Stdout + "\n" + ee.Error()
	}
	low := strings.ToLower(msg)

	// 注意：有些 "not available" 其实换 client 能好；因此 permanent 只保留更硬的信号。
	hardPermanent := []string{
		"private video",
		"copyright",
		"account associated with this video has been terminated",
		"join this channel to get access",
		"file is larger than max-filesize",
	}
	for _, p := range hardPermanent {
		if strings.Contains(low, p) {
			return false
		}
	}

	transient := []string{
		"http error 403",
		"http error 429",
		"http error 5",
		"too many requests",
		"timed out",
		"timeout",
		"temporar",
		"connection reset",
		"connection refused",
		"network is unreachable",
		"tls",
		"ssl",
		"eof",
		"fragment",
		"unable to download",
		"failed to extract",
		"failed to download",
		"no video formats",
		"requested format is not available",
		"sign in to confirm",
		"confirm you're not a bot",
		"this video is not available",
		"the page needs to be reloaded",
		"sabr",
		"po token",
		"only images are available",
		"js challenge",
		"ffmpeg",
		"postprocessing",
		"fake ytdlp failed", // 单测瞬时失败文案
	}
	for _, t := range transient {
		if strings.Contains(low, t) {
			return true
		}
	}
	// 未知错误：保守重试，换 client 成本可接受。
	return true
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
