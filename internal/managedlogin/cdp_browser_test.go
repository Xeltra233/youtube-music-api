package managedlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHardenProfilePreferencesPreservesExistingSettings(t *testing.T) {
	profile := t.TempDir()
	defaultDir := filepath.Join(profile, "Default")
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"homepage":"https://www.youtube.com/","profile":{"custom":true},"autofill":{"other":"keep"}}`
	path := filepath.Join(defaultDir, "Preferences")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hardenProfilePreferences(profile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var prefs map[string]any
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs["homepage"] != "https://www.youtube.com/" || prefs["credentials_enable_service"] != false || prefs["credentials_enable_autosignin"] != false {
		t.Fatalf("top-level prefs=%v", prefs)
	}
	profilePrefs, _ := prefs["profile"].(map[string]any)
	if profilePrefs["custom"] != true || profilePrefs["password_manager_enabled"] != false || profilePrefs["password_manager_leak_detection"] != false {
		t.Fatalf("profile prefs=%v", profilePrefs)
	}
	autofill, _ := prefs["autofill"].(map[string]any)
	if autofill["other"] != "keep" || autofill["profile_enabled"] != false || autofill["credit_card_enabled"] != false || autofill["enabled"] != false {
		t.Fatalf("autofill prefs=%v", autofill)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("preferences permissions=%v", info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(defaultDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") || strings.Contains(entry.Name(), ".bak-") {
			t.Fatalf("preference temporary file remained: %s", entry.Name())
		}
	}
}

func TestFilterManagedCookiesOnlyKeepsValidYouTubeGoogleEntries(t *testing.T) {
	filtered := filterManagedCookies([]BrowserCookie{
		{Name: "SID", Value: "sid", Domain: ".google.com", Path: "/"},
		{Name: "LOGIN_INFO", Value: "login", Domain: ".youtube.com", Path: ""},
		{Name: "foreign", Value: "x", Domain: ".example.com", Path: "/"},
		{Name: "bad\nname", Value: "x", Domain: ".youtube.com", Path: "/"},
		{Name: "empty", Value: "", Domain: ".youtube.com", Path: "/"},
	})
	if len(filtered) != 2 {
		t.Fatalf("filtered=%+v", filtered)
	}
	for _, cookie := range filtered {
		if !isYouTubeGoogleDomain(cookie.Domain) || cookie.Path != "/" {
			t.Fatalf("cookie=%+v", cookie)
		}
	}
}

func TestValidateInputBoundsAndSensitiveTextLength(t *testing.T) {
	valid := []InputEvent{
		{Kind: "mouse", EventType: "mousePressed", X: 0.5, Y: 0.5, Button: "left", ClickCount: 1},
		{Kind: "key", EventType: "keyDown", Key: "Enter", Code: "Enter"},
		{Kind: "text", Text: "password-value"},
	}
	for _, event := range valid {
		if err := validateInput(event); err != nil {
			t.Fatalf("valid event %+v: %v", event, err)
		}
	}
	invalid := []InputEvent{
		{Kind: "mouse", EventType: "mousePressed", X: -0.1, Y: 0.5},
		{Kind: "mouse", EventType: "script", X: 0.5, Y: 0.5},
		{Kind: "key", EventType: "execute", Key: "x"},
		{Kind: "key", EventType: "keyDown", Key: "x", WindowsKey: 70000},
		{Kind: "text", Text: strings.Repeat("x", 4097)},
		{Kind: "text", Text: "bad\x00value"},
	}
	for _, event := range invalid {
		if err := validateInput(event); err == nil {
			t.Fatalf("invalid event accepted: %+v", event)
		}
	}
}

func TestResolveBrowserExecutableExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "browser-fixture")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveBrowserExecutable(path)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(path) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	if _, err := resolveBrowserExecutable(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing explicit browser path should fail")
	}
}

func TestScreencastBufferKeepsOnlyLatestFrame(t *testing.T) {
	browser := &cdpBrowser{frames: make(chan Frame, 1)}
	browser.offerFrame(Frame{Data: []byte("old")})
	browser.offerFrame(Frame{Data: []byte("new")})
	select {
	case frame := <-browser.frames:
		if !bytes.Equal(frame.Data, []byte("new")) {
			t.Fatalf("frame=%q", frame.Data)
		}
	default:
		t.Fatal("latest frame missing")
	}
}
