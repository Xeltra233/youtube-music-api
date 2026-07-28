// Package adminauth provides signed cookie sessions for the cookie-upload admin UI.
package adminauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// CookieName is the browser session cookie for admin UI.
	CookieName = "ytm_admin_session"
	// DefaultTTL used when caller passes a non-positive duration.
	DefaultTTL = 12 * time.Hour
)

var (
	// ErrDisabled means ADMIN_PASSWORD is empty.
	ErrDisabled = errors.New("adminauth: admin disabled")
	// ErrBadPassword is returned on login mismatch.
	ErrBadPassword = errors.New("adminauth: bad password")
	// ErrBadToken means the session cookie is missing/invalid/expired.
	ErrBadToken = errors.New("adminauth: bad token")
)

// Manager verifies the admin password and issues HMAC session tokens.
type Manager struct {
	password string
	secret   []byte
	ttl      time.Duration
	now      func() time.Time
}

// Options configures a Manager.
type Options struct {
	Password      string
	SessionSecret string
	TTL           time.Duration
	Now           func() time.Time
}

// New creates a Manager. Empty password => disabled admin.
func New(opts Options) *Manager {
	pass := strings.TrimSpace(opts.Password)
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	secret := strings.TrimSpace(opts.SessionSecret)
	var sec []byte
	if secret != "" {
		sum := sha256.Sum256([]byte(secret))
		sec = sum[:]
	} else if pass != "" {
		// Derive a stable process secret from password when not provided.
		sum := sha256.Sum256([]byte("ytmusic-bridge-admin-v1|" + pass))
		sec = sum[:]
	} else {
		// Disabled; keep random secret unused.
		sec = make([]byte, 32)
		_, _ = rand.Read(sec)
	}
	return &Manager{
		password: pass,
		secret:   sec,
		ttl:      ttl,
		now:      now,
	}
}

// Enabled reports whether admin login is configured.
func (m *Manager) Enabled() bool {
	return m != nil && strings.TrimSpace(m.password) != ""
}

// TTL returns the session lifetime.
func (m *Manager) TTL() time.Duration {
	if m == nil || m.ttl <= 0 {
		return DefaultTTL
	}
	return m.ttl
}

// Login checks password and returns a signed session token.
func (m *Manager) Login(password string) (token string, err error) {
	if !m.Enabled() {
		return "", ErrDisabled
	}
	if !secureEqualString(password, m.password) {
		return "", ErrBadPassword
	}
	return m.issue(m.now().Add(m.TTL()))
}

// Validate returns nil if token is a valid unexpired session.
func (m *Manager) Validate(token string) error {
	if !m.Enabled() {
		return ErrDisabled
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrBadToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrBadToken
	}
	expStr, nonce, sig := parts[0], parts[1], parts[2]
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || expUnix <= 0 {
		return ErrBadToken
	}
	if m.now().Unix() > expUnix {
		return ErrBadToken
	}
	if _, err := hex.DecodeString(nonce); err != nil || len(nonce) < 16 {
		return ErrBadToken
	}
	want := m.sign(expStr, nonce)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return ErrBadToken
	}
	return nil
}

func (m *Manager) issue(exp time.Time) (string, error) {
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return "", fmt.Errorf("adminauth: rand: %w", err)
	}
	nonce := hex.EncodeToString(nb[:])
	expStr := strconv.FormatInt(exp.Unix(), 10)
	sig := m.sign(expStr, nonce)
	return expStr + "." + nonce + "." + sig, nil
}

func (m *Manager) sign(expStr, nonce string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(expStr))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(nonce))
	sum := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum)
}

func secureEqualString(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
