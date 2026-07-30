package managedlogin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	devToolsActivePortFile = "DevToolsActivePort"
	maxScreencastBytes     = 8 << 20
)

// CDPLauncher starts Chromium with a loopback-only ephemeral DevTools port.
type CDPLauncher struct{}

func NewCDPLauncher() *CDPLauncher { return &CDPLauncher{} }

type cdpBrowser struct {
	client    *cdpClient
	cmd       *exec.Cmd
	frames    chan Frame
	done      chan error
	viewport  Viewport
	viewportM sync.RWMutex
	closeOnce sync.Once
}

func (l *CDPLauncher) Launch(ctx context.Context, opt LaunchOptions) (Browser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	executable, err := resolveBrowserExecutable(opt.ExecutablePath)
	if err != nil {
		return nil, err
	}
	if !validViewport(opt.Viewport) {
		opt.Viewport = Viewport{Width: defaultWidth, Height: defaultHeight}
	}
	absProfile, err := filepath.Abs(strings.TrimSpace(opt.ProfileDir))
	if err != nil || strings.TrimSpace(opt.ProfileDir) == "" {
		return nil, browserStageError("profile_dir")
	}
	opt.ProfileDir = absProfile
	if !validStartURL(opt.StartURL) {
		return nil, browserStageError("start_url")
	}
	if err := ensureProfileDir(opt.ProfileDir); err != nil {
		return nil, browserStageError("profile_dir")
	}
	if err := hardenProfilePreferences(opt.ProfileDir); err != nil {
		return nil, browserStageError("profile_preferences")
	}
	activePortPath := filepath.Join(opt.ProfileDir, devToolsActivePortFile)
	if err := os.Remove(activePortPath); err != nil && !os.IsNotExist(err) {
		return nil, browserStageError("devtools_port_cleanup")
	}

	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--user-data-dir=" + opt.ProfileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-mode",
		"--disable-breakpad",
		"--disable-component-update",
		"--disable-crash-reporter",
		"--disable-dev-shm-usage",
		"--disable-features=AutofillServerCommunication,PasswordManagerOnboarding,PasswordManagerRedesign",
		"--disable-save-password-bubble",
		"--disable-search-engine-choice-screen",
		"--disable-sync",
		"--disable-translate",
		"--metrics-recording-only",
		"--no-service-autorun",
		"--window-size=" + strconv.Itoa(opt.Viewport.Width) + "," + strconv.Itoa(opt.Viewport.Height),
		"about:blank",
	}
	if opt.Headless {
		args = append([]string{"--headless=new"}, args...)
	}
	// Keep the browser process under Browser.Close rather than letting
	// exec.CommandContext issue an immediate hard kill. A graceful CDP close is
	// important because Chromium flushes the persistent login profile while
	// shutting down.
	cmd := exec.Command(executable, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, browserStageError("process_start")
	}

	port, _, err := waitDevToolsPort(ctx, activePortPath, 15*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, browserStageError("devtools_port")
	}
	pageWS, err := waitPageWebSocket(ctx, port, 10*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, browserStageError("page_target")
	}
	client, err := dialCDP(ctx, pageWS)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, browserStageError("cdp_dial")
	}
	b := &cdpBrowser{
		client:   client,
		cmd:      cmd,
		frames:   make(chan Frame, 1),
		done:     make(chan error, 1),
		viewport: opt.Viewport,
	}
	client.setEventHandler(b.handleEvent)
	go b.waitProcess()
	go b.closeOnContext(ctx)

	setupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := client.Call(setupCtx, "Page.enable", map[string]any{}, nil); err != nil {
		_ = b.Close(context.Background())
		return nil, browserStageError("page_enable")
	}
	if err := client.Call(setupCtx, "Runtime.enable", map[string]any{}, nil); err != nil {
		_ = b.Close(context.Background())
		return nil, browserStageError("runtime_enable")
	}
	if err := b.Resize(setupCtx, opt.Viewport); err != nil {
		_ = b.Close(context.Background())
		return nil, browserStageError("viewport")
	}
	if err := client.Call(setupCtx, "Page.navigate", map[string]any{"url": opt.StartURL}, nil); err != nil {
		_ = b.Close(context.Background())
		return nil, browserStageError("navigate")
	}
	if err := waitForDocumentReady(setupCtx, client); err != nil {
		_ = b.Close(context.Background())
		return nil, browserStageError("page_ready")
	}
	if opt.Screencast {
		if opt.Headless {
			_ = client.Call(setupCtx, "Page.bringToFront", map[string]any{}, nil)
		}
		if err := startScreencast(setupCtx, client, opt.Viewport); err != nil {
			_ = b.Close(context.Background())
			return nil, browserStageCause("screencast", err)
		}
	}
	return b, nil
}

func (b *cdpBrowser) Frames() <-chan Frame { return b.frames }

func (b *cdpBrowser) Done() <-chan error { return b.done }

func (b *cdpBrowser) Viewport() Viewport {
	b.viewportM.RLock()
	defer b.viewportM.RUnlock()
	return b.viewport
}

func (b *cdpBrowser) Dispatch(ctx context.Context, event InputEvent) error {
	if b == nil || b.client == nil {
		return ErrBrowserClosed
	}
	if err := validateInput(event); err != nil {
		return err
	}
	switch event.Kind {
	case "mouse":
		vp := b.Viewport()
		button := event.Button
		if button == "" {
			button = "none"
		}
		return b.client.Call(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type":       event.EventType,
			"x":          event.X * float64(vp.Width),
			"y":          event.Y * float64(vp.Height),
			"button":     button,
			"buttons":    event.Buttons,
			"clickCount": event.ClickCount,
			"deltaX":     event.DeltaX,
			"deltaY":     event.DeltaY,
			"modifiers":  event.Modifiers,
		}, nil)
	case "key":
		params := map[string]any{
			"type":                  event.EventType,
			"key":                   event.Key,
			"code":                  event.Code,
			"text":                  event.Text,
			"windowsVirtualKeyCode": event.WindowsKey,
			"modifiers":             event.Modifiers,
		}
		err := b.client.Call(ctx, "Input.dispatchKeyEvent", params, nil)
		event.Text = ""
		return err
	case "text":
		text := event.Text
		err := b.client.Call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
		text = ""
		event.Text = ""
		return err
	default:
		return ErrInputRejected
	}
}

func (b *cdpBrowser) Resize(ctx context.Context, viewport Viewport) error {
	if b == nil || b.client == nil || !validViewport(viewport) {
		return ErrInputRejected
	}
	if err := b.client.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             viewport.Width,
		"height":            viewport.Height,
		"deviceScaleFactor": 1,
		"mobile":            false,
	}, nil); err != nil {
		return err
	}
	b.viewportM.Lock()
	b.viewport = viewport
	b.viewportM.Unlock()
	return nil
}

func (b *cdpBrowser) ExportCookies(ctx context.Context) ([]BrowserCookie, error) {
	if b == nil || b.client == nil {
		return nil, ErrBrowserClosed
	}
	var response struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
			Session  bool    `json:"session"`
		} `json:"cookies"`
	}
	if err := b.client.Call(ctx, "Storage.getCookies", map[string]any{}, &response); err != nil {
		return nil, err
	}
	out := make([]BrowserCookie, 0, len(response.Cookies))
	for _, c := range response.Cookies {
		out = append(out, BrowserCookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			Expires: c.Expires, HTTPOnly: c.HTTPOnly, Secure: c.Secure, Session: c.Session,
		})
	}
	return out, nil
}

func (b *cdpBrowser) ClearCookies(ctx context.Context) error {
	if b == nil || b.client == nil {
		return ErrBrowserClosed
	}
	return b.client.Call(ctx, "Storage.clearCookies", map[string]any{}, nil)
}

func (b *cdpBrowser) Close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.closeOnce.Do(func() {
		closeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		_ = b.client.Call(closeCtx, "Page.stopScreencast", map[string]any{}, nil)
		_ = b.client.Call(closeCtx, "Browser.close", map[string]any{}, nil)
		cancel()
	})
	select {
	case <-ctx.Done():
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		return ctx.Err()
	case <-b.done:
		return nil
	case <-time.After(2 * time.Second):
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		select {
		case <-b.done:
		case <-time.After(time.Second):
		}
		return nil
	}
}

func (b *cdpBrowser) waitProcess() {
	err := b.cmd.Wait()
	b.client.Close()
	if err != nil {
		err = ErrBrowserClosed
	}
	b.done <- err
	close(b.done)
}

func (b *cdpBrowser) closeOnContext(ctx context.Context) {
	if b == nil || ctx == nil {
		return
	}
	select {
	case <-ctx.Done():
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = b.Close(closeCtx)
		cancel()
	case <-b.done:
	}
}

func (b *cdpBrowser) handleEvent(method string, raw json.RawMessage) {
	if method != "Page.screencastFrame" {
		return
	}
	var event struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
		Metadata  struct {
			DeviceWidth  float64 `json:"deviceWidth"`
			DeviceHeight float64 `json:"deviceHeight"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return
	}
	_ = b.client.Send("Page.screencastFrameAck", map[string]any{"sessionId": event.SessionID})
	if event.Data == "" || base64.StdEncoding.DecodedLen(len(event.Data)) > maxScreencastBytes {
		return
	}
	data, err := base64.StdEncoding.DecodeString(event.Data)
	if err != nil || len(data) == 0 || len(data) > maxScreencastBytes {
		return
	}
	vp := b.Viewport()
	if w, h := int(event.Metadata.DeviceWidth), int(event.Metadata.DeviceHeight); validViewport(Viewport{Width: w, Height: h}) {
		vp = Viewport{Width: w, Height: h}
		b.viewportM.Lock()
		b.viewport = vp
		b.viewportM.Unlock()
	}
	b.offerFrame(Frame{Data: data, Viewport: vp})
}

func (b *cdpBrowser) offerFrame(frame Frame) {
	if b == nil || b.frames == nil || len(frame.Data) == 0 {
		return
	}
	select {
	case b.frames <- frame:
	default:
		select {
		case <-b.frames:
		default:
		}
		select {
		case b.frames <- frame:
		default:
		}
	}
}

type devToolsTarget struct {
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
}

func waitDevToolsPort(ctx context.Context, path string, timeout time.Duration) (int, string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		data, err := os.ReadFile(path)
		if err == nil && len(data) < 4096 {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 {
				port, parseErr := strconv.Atoi(strings.TrimSpace(lines[0]))
				browserPath := strings.TrimSpace(lines[1])
				if parseErr == nil && port > 0 && port <= 65535 && strings.HasPrefix(browserPath, "/devtools/browser/") {
					return port, browserPath, nil
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, "", ErrBrowserProtocol
}

func waitPageWebSocket(ctx context.Context, port int, timeout time.Duration) (string, error) {
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(timeout)
	created := false
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/list", nil)
		resp, err := client.Do(req)
		if err == nil {
			var targets []devToolsTarget
			if resp.StatusCode == http.StatusOK {
				_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&targets)
			}
			_ = resp.Body.Close()
			for _, target := range targets {
				if target.Type == "page" && target.WebSocketDebuggerURL != "" {
					return loopbackWebSocketURL(target.WebSocketDebuggerURL, port)
				}
			}
		}
		if !created {
			createReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, base+"/json/new?about%3Ablank", nil)
			if createResp, createErr := client.Do(createReq); createErr == nil {
				var target devToolsTarget
				if createResp.StatusCode == http.StatusOK {
					created = true
					_ = json.NewDecoder(io.LimitReader(createResp.Body, 1<<20)).Decode(&target)
				}
				_ = createResp.Body.Close()
				if target.WebSocketDebuggerURL != "" {
					return loopbackWebSocketURL(target.WebSocketDebuggerURL, port)
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", ErrBrowserProtocol
}

func loopbackWebSocketURL(raw string, port int) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || !strings.HasPrefix(u.Path, "/devtools/") {
		return "", ErrBrowserProtocol
	}
	u.Scheme = "ws"
	u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func validStartURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "google.com" || strings.HasSuffix(host, ".google.com")
}

func resolveBrowserExecutable(configured string) (string, error) {
	configured = strings.Trim(strings.TrimSpace(configured), `"`)
	if configured != "" {
		if filepath.IsAbs(configured) || strings.ContainsAny(configured, `/\`) {
			path, err := filepath.Abs(configured)
			if err == nil {
				if st, statErr := os.Stat(path); statErr == nil && !st.IsDir() {
					return path, nil
				}
			}
			return "", os.ErrNotExist
		}
		if path, err := exec.LookPath(configured); err == nil {
			return path, nil
		}
		return "", os.ErrNotExist
	}

	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	for _, path := range commonBrowserPaths() {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func commonBrowserPaths() []string {
	paths := []string{}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			)
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			matches, _ := filepath.Glob(filepath.Join(local, "ms-playwright", "chromium-*", "chrome-win64", "chrome.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			paths = append(paths, matches...)
		}
	}
	if runtime.GOOS == "darwin" {
		paths = append(paths,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	}
	return paths
}

func hardenProfilePreferences(profileDir string) error {
	defaultDir := filepath.Join(profileDir, "Default")
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(defaultDir, 0o700)
	path := filepath.Join(defaultDir, "Preferences")
	prefs := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if len(data) > 16<<20 || json.Unmarshal(data, &prefs) != nil {
			return ErrBrowserProtocol
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	prefs["credentials_enable_service"] = false
	prefs["credentials_enable_autosignin"] = false
	setPreferenceMap(prefs, "profile", map[string]any{
		"password_manager_enabled":        false,
		"password_manager_leak_detection": false,
	})
	setPreferenceMap(prefs, "autofill", map[string]any{
		"profile_enabled":     false,
		"credit_card_enabled": false,
		"enabled":             false,
	})
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(path, data)
}

func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
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
	if err := f.Chmod(0o600); err != nil {
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
	if err := os.Rename(tmp, path); err == nil {
		keepTmp = false
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return ErrBrowserProtocol
	}
	backupFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".bak-*")
	if err != nil {
		return err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backup)
		return err
	}
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		rollbackErr := os.Rename(backup, path)
		return errors.Join(err, rollbackErr)
	}
	keepTmp = false
	_ = os.Remove(backup)
	return nil
}

func setPreferenceMap(root map[string]any, key string, values map[string]any) {
	nested, _ := root[key].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}
	for k, v := range values {
		nested[k] = v
	}
	root[key] = nested
}

func startScreencast(ctx context.Context, client *cdpClient, viewport Viewport) error {
	params := map[string]any{
		"format":        "jpeg",
		"quality":       82,
		"maxWidth":      viewport.Width,
		"maxHeight":     viewport.Height,
		"everyNthFrame": 1,
	}
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for {
		last = client.Call(ctx, "Page.startScreencast", params, nil)
		if last == nil {
			return nil
		}
		var protocolErr *cdpCallError
		if !errors.As(last, &protocolErr) || protocolErr.code != -32000 || time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForDocumentReady(ctx context.Context, client *cdpClient) error {
	for {
		var response struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		err := client.Call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "JSON.stringify({state:document.readyState,host:location.hostname})",
			"returnByValue": true,
			"awaitPromise":  false,
		}, &response)
		var ready struct {
			State string `json:"state"`
			Host  string `json:"host"`
		}
		_ = json.Unmarshal([]byte(response.Result.Value), &ready)
		if err == nil && validReadyHost(ready.Host) && (ready.State == "interactive" || ready.State == "complete") {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validReadyHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "google.com" || strings.HasSuffix(host, ".google.com")
}

func browserStageError(stage string) error {
	return fmt.Errorf("%w: %s", ErrBrowserProtocol, stage)
}

func browserStageCause(stage string, cause error) error {
	var protocolErr *cdpCallError
	if errors.As(cause, &protocolErr) {
		return fmt.Errorf("%w: %s code=%d", ErrBrowserProtocol, stage, protocolErr.code)
	}
	return browserStageError(stage)
}
