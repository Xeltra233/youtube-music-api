package adminauth

import (
	"testing"
	"time"
)

func TestLoginAndValidate(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := New(Options{
		Password:      "s3cret",
		SessionSecret: "sess",
		TTL:           time.Hour,
		Now:           func() time.Time { return now },
	})
	if !m.Enabled() {
		t.Fatal("enabled")
	}
	tok, err := m.Login("s3cret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := m.Validate(tok); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := m.Login("wrong"); err != ErrBadPassword {
		t.Fatalf("want bad password, got %v", err)
	}
}

func TestTokenExpires(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	clock := now
	m := New(Options{
		Password: "x",
		TTL:      10 * time.Minute,
		Now:      func() time.Time { return clock },
	})
	tok, err := m.Login("x")
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(11 * time.Minute)
	if err := m.Validate(tok); err != ErrBadToken {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestDisabled(t *testing.T) {
	m := New(Options{Password: ""})
	if m.Enabled() {
		t.Fatal("should be disabled")
	}
	if _, err := m.Login("x"); err != ErrDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	m := New(Options{Password: "p", SessionSecret: "s", TTL: time.Hour})
	tok, err := m.Login("p")
	if err != nil {
		t.Fatal(err)
	}
	// flip last char
	bad := tok[:len(tok)-1] + "A"
	if bad == tok {
		bad = tok[:len(tok)-1] + "B"
	}
	if err := m.Validate(bad); err != ErrBadToken {
		t.Fatalf("want bad token, got %v", err)
	}
}
