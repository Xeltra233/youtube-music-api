package managedlogin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveCDPManagedBrowser is opt-in because it needs a local Chromium-family
// executable and network access. It records only counts/sizes through test
// assertions; no frames, cookies, profile contents, or input are persisted.
func TestLiveCDPManagedBrowser(t *testing.T) {
	if os.Getenv("YTM_LIVE_CDP") != "1" {
		t.Skip("set YTM_LIVE_CDP=1 to run the local headless CDP probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	firstCtx, firstCancel := context.WithCancel(ctx)
	defer firstCancel()
	profileDir := filepath.Join(t.TempDir(), "profile")
	browser, err := NewCDPLauncher().Launch(firstCtx, LaunchOptions{
		ExecutablePath: os.Getenv("YOUTUBE_LOGIN_BROWSER_PATH"),
		ProfileDir:     profileDir,
		Headless:       true,
		Viewport:       Viewport{Width: 1024, Height: 720},
		StartURL:       refreshURL,
		Screencast:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close(context.Background())

	select {
	case frame := <-browser.Frames():
		if len(frame.Data) == 0 || !validViewport(frame.Viewport) {
			t.Fatalf("invalid frame metadata: bytes=%d viewport=%+v", len(frame.Data), frame.Viewport)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("screencast frame timeout")
	}
	if _, err := browser.ExportCookies(ctx); err != nil {
		t.Fatal(err)
	}
	if err := browser.Dispatch(ctx, InputEvent{
		Kind: "mouse", EventType: "mouseMoved", X: 0.5, Y: 0.5,
	}); err != nil {
		t.Fatal(err)
	}
	implementation, ok := browser.(*cdpBrowser)
	if !ok {
		t.Fatal("unexpected live browser implementation")
	}
	const persistentMarker = "YTM_LIVE_PROFILE_MARKER"
	if err := implementation.client.Call(ctx, "Storage.setCookies", map[string]any{
		"cookies": []map[string]any{{
			"name": persistentMarker, "value": "fixture", "domain": ".youtube.com", "path": "/",
			"secure": true, "httpOnly": true, "expires": float64(time.Now().Add(time.Hour).Unix()),
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	// Cancel the launch context instead of calling Close directly. The launcher
	// must translate cancellation into a graceful CDP shutdown so Chromium has
	// time to flush the dedicated profile.
	firstCancel()
	select {
	case <-browser.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not close after context cancellation")
	}
	if _, err := os.Stat(filepath.Join(profileDir, "Local State")); err != nil {
		t.Fatalf("persistent profile was not written: %v", err)
	}

	refreshBrowser, err := NewCDPLauncher().Launch(ctx, LaunchOptions{
		ExecutablePath: os.Getenv("YOUTUBE_LOGIN_BROWSER_PATH"),
		ProfileDir:     profileDir,
		Headless:       true,
		Viewport:       Viewport{Width: 1024, Height: 720},
		StartURL:       refreshURL,
		Screencast:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshedCookies, err := refreshBrowser.ExportCookies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	markerFound := false
	for _, cookie := range refreshedCookies {
		if cookie.Name == persistentMarker && isYouTubeGoogleDomain(cookie.Domain) {
			markerFound = true
			break
		}
	}
	if !markerFound {
		t.Fatal("persistent profile marker was not restored")
	}
	refreshCloseCtx, refreshCloseCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := refreshBrowser.Close(refreshCloseCtx); err != nil {
		refreshCloseCancel()
		t.Fatal(err)
	}
	refreshCloseCancel()
}
