package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xeltra/ytmusic-bridge/internal/adminauth"
	"github.com/xeltra/ytmusic-bridge/internal/managedlogin"
)

const (
	maxLoginChannelMessage  = 16 << 10
	maxLoginEventsPerSecond = 240
)

type loginChannelMessage struct {
	Type       string  `json:"type"`
	EventType  string  `json:"event_type"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	Buttons    int     `json:"buttons"`
	ClickCount int     `json:"click_count"`
	DeltaX     float64 `json:"delta_x"`
	DeltaY     float64 `json:"delta_y"`
	Modifiers  int     `json:"modifiers"`
	Key        string  `json:"key"`
	Code       string  `json:"code"`
	Text       string  `json:"text"`
	WindowsKey int     `json:"windows_key_code"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
}

func (s *Server) handleYouTubeLoginCreate(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	snapshot, err := s.managedLogin.Create(s.adminOwner(r))
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "session": snapshot})
}

func (s *Server) handleYouTubeLoginGet(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	snapshot, err := s.managedLogin.Get(s.adminOwner(r), r.PathValue("id"))
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": snapshot})
}

func (s *Server) handleYouTubeLoginDelete(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	snapshot, err := s.managedLogin.Terminate(s.adminOwner(r), r.PathValue("id"))
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": snapshot})
}

func (s *Server) handleYouTubeLoginVerify(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	snapshot, err := s.managedLogin.Verify(s.adminOwner(r), r.PathValue("id"))
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": snapshot})
}

func (s *Server) handleYouTubeLoginDisconnect(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	err := s.managedLogin.Disconnect(ctx, s.adminOwner(r))
	cancel()
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "disconnected": true})
}

func (s *Server) handleYouTubeLoginChannel(w http.ResponseWriter, r *http.Request) {
	setLoginNoStore(w)
	if !s.requireAdmin(w, r) {
		return
	}
	if s.managedLogin == nil {
		writeManagedLoginError(w, http.StatusServiceUnavailable, "managed_disabled", "浏览器登录尚未启用")
		return
	}
	if !exactSameOrigin(r) {
		writeManagedLoginError(w, http.StatusForbidden, "origin_mismatch", "请求来源校验失败")
		return
	}
	control, err := s.managedLogin.AcquireControl(s.adminOwner(r), r.PathValue("id"))
	if err != nil {
		writeManagedLoginMappedError(w, err)
		return
	}
	defer control.Close()

	upgrader := websocket.Upgrader{
		HandshakeTimeout:  10 * time.Second,
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		EnableCompression: false,
		CheckOrigin:       exactSameOrigin,
	}
	conn, err := upgrader.Upgrade(w, r, http.Header{
		"Cache-Control":          []string{"no-store"},
		"Pragma":                 []string{"no-cache"},
		"X-Content-Type-Options": []string{"nosniff"},
	})
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxLoginChannelMessage)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})

	lastViewport := managedlogin.Viewport{}
	if snapshot, _ := control.Snapshot(); snapshot.ID != "" {
		lastViewport = snapshot.Viewport
		if writeLoginChannelJSON(conn, map[string]any{"type": "status", "session": snapshot}) != nil {
			return
		}
		if writeLoginChannelJSON(conn, map[string]any{
			"type": "viewport", "width": snapshot.Viewport.Width, "height": snapshot.Viewport.Height,
		}) != nil {
			return
		}
	}

	messages := make(chan loginChannelMessage, 32)
	readDone := make(chan struct{})
	go readLoginChannel(conn, messages, readDone)
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	frames := control.Frames()
	for {
		select {
		case <-readDone:
			return
		case <-control.Done():
			if snapshot, _ := control.Snapshot(); snapshot.ID != "" {
				_ = writeLoginChannelJSON(conn, map[string]any{"type": "status", "session": snapshot})
			}
			return
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			if len(frame.Data) == 0 {
				continue
			}
			if frame.Viewport.Width > 0 && frame.Viewport.Height > 0 && frame.Viewport != lastViewport {
				lastViewport = frame.Viewport
				if writeLoginChannelJSON(conn, map[string]any{
					"type": "viewport", "width": lastViewport.Width, "height": lastViewport.Height,
				}) != nil {
					return
				}
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.BinaryMessage, frame.Data); err != nil {
				return
			}
		case message := <-messages:
			stop := s.handleLoginChannelMessage(conn, control, &message)
			message.Text = ""
			message.Key = ""
			message.Code = ""
			if stop {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleLoginChannelMessage(conn *websocket.Conn, control *managedlogin.Control, message *loginChannelMessage) bool {
	if message == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch message.Type {
	case "mouse", "key", "text":
		err := control.Dispatch(ctx, managedlogin.InputEvent{
			Kind: message.Type, EventType: message.EventType,
			X: message.X, Y: message.Y, Button: message.Button, Buttons: message.Buttons,
			ClickCount: message.ClickCount, DeltaX: message.DeltaX, DeltaY: message.DeltaY,
			Modifiers: message.Modifiers, Key: message.Key, Code: message.Code,
			Text: message.Text, WindowsKey: message.WindowsKey,
		})
		if err != nil {
			_ = writeLoginChannelJSON(conn, map[string]any{"type": "error", "code": "input_rejected"})
		}
	case "resize":
		err := control.Resize(ctx, managedlogin.Viewport{Width: message.Width, Height: message.Height})
		if err != nil {
			_ = writeLoginChannelJSON(conn, map[string]any{"type": "error", "code": "resize_rejected"})
			return false
		}
		_ = writeLoginChannelJSON(conn, map[string]any{"type": "viewport", "width": message.Width, "height": message.Height})
	case "verify":
		snapshot, err := control.Verify()
		if err != nil {
			code, _ := managedLoginError(err)
			_ = writeLoginChannelJSON(conn, map[string]any{"type": "error", "code": code})
			_ = writeLoginChannelJSON(conn, map[string]any{"type": "status", "session": snapshot})
			return false
		}
		_ = writeLoginChannelJSON(conn, map[string]any{"type": "status", "session": snapshot})
		return snapshot.State == managedlogin.StateSynced
	case "terminate":
		snapshot, _ := control.Terminate()
		_ = writeLoginChannelJSON(conn, map[string]any{"type": "status", "session": snapshot})
		return true
	default:
		_ = writeLoginChannelJSON(conn, map[string]any{"type": "error", "code": "message_rejected"})
	}
	return false
}

func readLoginChannel(conn *websocket.Conn, messages chan<- loginChannelMessage, done chan<- struct{}) {
	defer close(done)
	windowStart := time.Now()
	windowEvents := 0
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage || len(data) == 0 || len(data) > maxLoginChannelMessage {
			return
		}
		now := time.Now()
		if now.Sub(windowStart) >= time.Second {
			windowStart = now
			windowEvents = 0
		}
		windowEvents++
		if windowEvents > maxLoginEventsPerSecond {
			zeroBytes(data)
			return
		}
		var message loginChannelMessage
		if err := json.Unmarshal(data, &message); err != nil {
			zeroBytes(data)
			return
		}
		zeroBytes(data)
		select {
		case messages <- message:
		default:
			message.Text = ""
			return
		}
	}
}

func writeLoginChannelJSON(conn *websocket.Conn, value any) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteJSON(value)
}

func (s *Server) adminOwner(r *http.Request) string {
	return adminauth.Fingerprint(s.adminSessionToken(r))
}

func exactSameOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" || strings.Contains(raw, ",") {
		return false
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Path != "" && origin.Path != "/") {
		return false
	}
	scheme := requestScheme(r)
	requestHost, err := url.Parse("//" + r.Host)
	if err != nil || requestHost.User != nil || requestHost.Hostname() == "" {
		return false
	}
	return strings.EqualFold(origin.Scheme, scheme) &&
		strings.EqualFold(origin.Hostname(), requestHost.Hostname()) &&
		normalizedOriginPort(origin.Port(), scheme) == normalizedOriginPort(requestHost.Port(), scheme)
}

func normalizedOriginPort(port, scheme string) string {
	if port != "" {
		return port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func setLoginNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeManagedLoginMappedError(w http.ResponseWriter, err error) {
	code, status := managedLoginError(err)
	message := map[string]string{
		"managed_disabled": "浏览器登录尚未启用",
		"session_busy":     "已有浏览器登录会话正在运行",
		"session_missing":  "登录会话不存在",
		"session_expired":  "登录会话已过期",
		"browser_starting": "浏览器仍在启动",
		"control_busy":     "该登录会话已有控制连接",
		"verify_busy":      "登录状态正在校验",
		"not_logged_in":    "尚未识别到 YouTube 登录状态",
		"sync_failed":      "Cookie 同步失败",
	}[code]
	if message == "" {
		message = "浏览器登录请求失败"
	}
	writeManagedLoginError(w, status, code, message)
}

func managedLoginError(err error) (string, int) {
	switch {
	case errors.Is(err, managedlogin.ErrDisabled):
		return "managed_disabled", http.StatusServiceUnavailable
	case errors.Is(err, managedlogin.ErrBusy):
		return "session_busy", http.StatusConflict
	case errors.Is(err, managedlogin.ErrNotFound):
		return "session_missing", http.StatusNotFound
	case errors.Is(err, managedlogin.ErrExpired):
		return "session_expired", http.StatusGone
	case errors.Is(err, managedlogin.ErrNotReady), errors.Is(err, managedlogin.ErrBrowserClosed):
		return "browser_starting", http.StatusConflict
	case errors.Is(err, managedlogin.ErrControlBusy):
		return "control_busy", http.StatusConflict
	case errors.Is(err, managedlogin.ErrVerifyBusy):
		return "verify_busy", http.StatusConflict
	case errors.Is(err, managedlogin.ErrNotLoggedIn):
		return "not_logged_in", http.StatusConflict
	default:
		return "sync_failed", http.StatusInternalServerError
	}
}

func writeManagedLoginError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message},
	})
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
