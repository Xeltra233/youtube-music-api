package managedlogin

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type cdpEnvelope struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type cdpReply struct {
	result json.RawMessage
	code   int
	failed bool
}

type cdpCallError struct {
	method string
	code   int
}

func (e *cdpCallError) Error() string { return "managed browser protocol failed" }
func (e *cdpCallError) Unwrap() error { return ErrBrowserProtocol }

type cdpClient struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[int64]chan cdpReply
	handler   func(string, json.RawMessage)
	nextID    atomic.Int64
	done      chan struct{}
	closeOnce sync.Once
}

func dialCDP(ctx context.Context, wsURL string) (*cdpClient, error) {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, ErrBrowserProtocol
	}
	conn.SetReadLimit(16 << 20)
	c := &cdpClient{
		conn:    conn,
		pending: make(map[int64]chan cdpReply),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *cdpClient) setEventHandler(handler func(string, json.RawMessage)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *cdpClient) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil || c.conn == nil {
		return ErrBrowserClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := c.nextID.Add(1)
	replyCh := make(chan cdpReply, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrBrowserClosed
	default:
	}
	c.pending[id] = replyCh
	c.mu.Unlock()

	if err := c.writeCommand(id, method, params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return ErrBrowserClosed
	case reply := <-replyCh:
		if reply.failed {
			return &cdpCallError{method: method, code: reply.code}
		}
		if out != nil && len(reply.result) > 0 {
			if err := json.Unmarshal(reply.result, out); err != nil {
				return ErrBrowserProtocol
			}
		}
		return nil
	}
}

// Send emits a CDP command whose response is intentionally ignored. It is used
// for screencast acknowledgements from inside the sole reader goroutine.
func (c *cdpClient) Send(method string, params any) error {
	if c == nil || c.conn == nil {
		return ErrBrowserClosed
	}
	id := c.nextID.Add(1)
	return c.writeCommand(id, method, params)
}

func (c *cdpClient) writeCommand(id int64, method string, params any) error {
	payload := struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{ID: id, Method: method, Params: params}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return ErrBrowserProtocol
	}
	if err := c.conn.WriteJSON(payload); err != nil {
		return ErrBrowserClosed
	}
	return nil
}

func (c *cdpClient) readLoop() {
	defer c.shutdown()
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg cdpEnvelope
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				reply := cdpReply{result: msg.Result}
				if msg.Error != nil {
					reply.failed = true
					reply.code = msg.Error.Code
				}
				ch <- reply
			}
			continue
		}
		if msg.Method != "" {
			c.mu.Lock()
			handler := c.handler
			c.mu.Unlock()
			if handler != nil {
				handler(msg.Method, msg.Params)
			}
		}
	}
}

func (c *cdpClient) shutdown() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]chan cdpReply)
		c.mu.Unlock()
		for _, ch := range pending {
			select {
			case ch <- cdpReply{failed: true, code: -1}:
			default:
			}
		}
		_ = c.conn.Close()
	})
}

func (c *cdpClient) Close() {
	if c == nil {
		return
	}
	c.shutdown()
}
