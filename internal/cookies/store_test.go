package cookies

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleNetscape(login bool) string {
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	b.WriteString("# This is a generated test file\n\n")
	b.WriteString(".youtube.com	TRUE	/	FALSE	0	VISITOR_INFO1_LIVE	abc\n")
	if login {
		b.WriteString(".youtube.com	TRUE	/	TRUE	0	LOGIN_INFO	testlogin\n")
		b.WriteString(".youtube.com	TRUE	/	TRUE	0	__Secure-3PSID	sidvalue\n")
		b.WriteString(".youtube.com	TRUE	/	FALSE	0	SAPISID	sapisid\n")
	}
	b.WriteString(".google.com	TRUE	/	TRUE	0	SID	sid\n")
	return b.String()
}

func TestResolvePromotesDropInToStableName(t *testing.T) {
	dir := t.TempDir()
	drop := filepath.Join(dir, "966fac1c-2b8f-4521-a4b7-c4e284328da9.txt")
	if err := os.WriteFile(drop, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ResolveOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(dir, StableFileName)
	if filepath.Clean(got) != filepath.Clean(stable) {
		t.Fatalf("want stable %s, got %s", stable, got)
	}
	if !FileExistsNonEmpty(stable) {
		t.Fatal("stable file missing")
	}
}

func TestResolvePrefersExistingStable(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make stable newer so time-based dedup keeps its content.
	newer := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(stable, newer, newer); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte(sampleNetscape(false)), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(other, older, older); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ResolveOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(stable) {
		t.Fatalf("got %s", got)
	}
	// Older drop-in must be removed.
	if FileExistsNonEmpty(other) {
		t.Fatal("older drop-in should be deleted")
	}
}

func TestHeaderFromFileIncludesLoginCookies(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.txt")
	if err := os.WriteFile(p, []byte(sampleNetscape(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := HeaderFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "LOGIN_INFO=") {
		t.Fatalf("missing LOGIN_INFO in %q", h)
	}
	if !strings.Contains(h, "SOCS=CAI") {
		t.Fatalf("missing SOCS in %q", h)
	}
	// 不应把值打印到错误以外；这里只检查名字。
	if strings.Contains(h, "\t") {
		t.Fatal("header should not contain tabs")
	}
}

func TestResolveEmptyDirReturnsStablePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "cookies")
	got, err := Resolve(ResolveOptions{Dir: sub})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != StableFileName {
		t.Fatalf("got %s", got)
	}
	if st, err := os.Stat(sub); err != nil || !st.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestDeduplicateKeepsLaterByModTime(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")

	oldBody := sampleNetscape(true) + "# old-marker\n"
	newBody := sampleNetscape(true) + "# new-marker\n"
	if err := os.WriteFile(oldPath, []byte(oldBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(newBody), 0o600); err != nil {
		t.Fatal(err)
	}
	oldT := time.Now().Add(-3 * time.Hour)
	newT := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(oldPath, oldT, oldT); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newT, newT); err != nil {
		t.Fatal(err)
	}

	if err := Deduplicate(dir, stable); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-marker") {
		t.Fatalf("stable should keep later file content, got %q", data)
	}
	if strings.Contains(string(data), "old-marker") {
		t.Fatal("stable should not keep older content")
	}
	if FileExistsNonEmpty(oldPath) || FileExistsNonEmpty(newPath) {
		t.Fatal("drop-ins should be removed after dedup; only stable remains")
	}
}

func TestResolveOnRestartKeepsLaterOnly(t *testing.T) {
	// Simulates restart: multiple drop-ins already on disk, Resolve must keep later.
	dir := t.TempDir()
	early := filepath.Join(dir, "early.txt")
	late := filepath.Join(dir, "late.txt")
	if err := os.WriteFile(early, []byte(sampleNetscape(true)+"\n# early\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(late, []byte(sampleNetscape(true)+"\n# late\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-4 * time.Hour)
	t1 := time.Now().Add(30 * time.Minute)
	_ = os.Chtimes(early, t0, t0)
	_ = os.Chtimes(late, t1, t1)

	got, err := Resolve(ResolveOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(dir, StableFileName)
	if filepath.Clean(got) != filepath.Clean(stable) {
		t.Fatalf("want %s got %s", stable, got)
	}
	body, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "# late") {
		t.Fatalf("restart resolve should keep later content: %q", body)
	}
	if FileExistsNonEmpty(early) || FileExistsNonEmpty(late) {
		t.Fatal("restart resolve should delete older drop-ins")
	}
}

func TestRefreshDropInsOnUploadKeepsLater(t *testing.T) {
	// Simulates upload path: existing stable + newly uploaded later file.
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	if err := os.WriteFile(stable, []byte(sampleNetscape(true)+"\n# old-stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldT := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(stable, oldT, oldT)

	upload := filepath.Join(dir, "upload-demo.txt")
	if err := os.WriteFile(upload, []byte(sampleNetscape(true)+"\n# uploaded-later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newT := time.Now().Add(time.Hour)
	_ = os.Chtimes(upload, newT, newT)

	if err := RefreshDropIns(dir, stable); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "uploaded-later") {
		t.Fatalf("upload refresh should keep later file: %q", body)
	}
	if FileExistsNonEmpty(upload) {
		t.Fatal("uploaded drop-in should be removed after promote")
	}
}
