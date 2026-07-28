package cookies

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// KeepAliveOptions 控制周期性 yt-dlp cookie 回写保活。
type KeepAliveOptions struct {
	CookiesFile string
	YtdlpPath   string
	Proxy       string
	Interval    time.Duration
	// URLs 默认访问 YouTube / YouTube Music 首页以刷新 cookie jar。
	URLs []string
}

// RunKeepAliveLoop 在后台周期性执行保活，直到 ctx 取消。
// yt-dlp 的 --cookies 会“读入并写回”同一文件，从而延长登录态。
func RunKeepAliveLoop(ctx context.Context, opt KeepAliveOptions) {
	if strings.TrimSpace(opt.CookiesFile) == "" {
		return
	}
	interval := opt.Interval
	if interval < time.Minute {
		interval = time.Minute
	}
	// 启动后稍等再跑，避免和 boot 抢带宽；随后立即尝试一次。
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()

	run := func() {
		// 每次前重新 Resolve 不在这里做：调用方应传入稳定路径。
		// 若用户刚拷入文件，main 侧可在 loop 前再 resolve。
		if !FileExistsNonEmpty(opt.CookiesFile) {
			log.Printf("cookies keepalive: waiting for file %s", opt.CookiesFile)
			return
		}
		// Never let yt-dlp rewrite the stable jar in place.
		snap, cleanup, err := SnapshotForYtdlp(opt.CookiesFile)
		if err != nil {
			log.Printf("cookies keepalive: snapshot: %v", err)
			return
		}
		if snap == "" {
			log.Printf("cookies keepalive: waiting for file %s", opt.CookiesFile)
			return
		}
		runOpt := opt
		runOpt.CookiesFile = snap
		if err := KeepAliveOnce(ctx, runOpt); err != nil {
			cleanup()
			log.Printf("cookies keepalive: %v", err)
			return
		}
		if err := CommitSnapshotIfBetter(snap, opt.CookiesFile); err != nil {
			cleanup()
			log.Printf("cookies keepalive: commit: %v", err)
			return
		}
		cleanup()
		log.Printf("cookies keepalive: refreshed %s", opt.CookiesFile)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
			t.Reset(interval)
		}
	}
}

// KeepAliveOnce 调用一次 yt-dlp，让其读写 cookie jar。
func KeepAliveOnce(ctx context.Context, opt KeepAliveOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	path := strings.TrimSpace(opt.CookiesFile)
	if path == "" {
		return fmt.Errorf("cookies keepalive: empty cookies file")
	}
	ytdlp := strings.TrimSpace(opt.YtdlpPath)
	if ytdlp == "" {
		if p, err := exec.LookPath("yt-dlp"); err == nil {
			ytdlp = p
		} else if p, err := exec.LookPath("yt-dlp.exe"); err == nil {
			ytdlp = p
		} else {
			return fmt.Errorf("cookies keepalive: yt-dlp not found")
		}
	}
	urls := opt.URLs
	if len(urls) == 0 {
		urls = []string{
			"https://www.youtube.com/",
			"https://music.youtube.com/",
		}
	}

	var last error
	for _, u := range urls {
		if err := ctx.Err(); err != nil {
			return err
		}
		args := []string{
			"--skip-download",
			"--no-playlist",
			"--no-progress",
			"--quiet",
			"--no-warnings",
			"--retries", "2",
			"--socket-timeout", "20",
			"--geo-bypass",
			"--cookies", path,
			"--", u,
		}
		if strings.TrimSpace(opt.Proxy) != "" {
			args = append([]string{"--proxy", strings.TrimSpace(opt.Proxy)}, args...)
		}
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		cmd := exec.CommandContext(cctx, ytdlp, args...)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if len(msg) > 400 {
				msg = msg[:400] + "..."
			}
			if msg == "" {
				msg = err.Error()
			}
			last = fmt.Errorf("yt-dlp keepalive %s: %s", u, msg)
			continue
		}
		// 成功一次即可
		return nil
	}
	if last != nil {
		return last
	}
	return fmt.Errorf("cookies keepalive: no urls")
}

// TouchDirForMount 仅创建目录，方便镜像/本地默认挂载点存在。
func TouchDirForMount(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "cookies"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return nil
}
