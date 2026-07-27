package cookies

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte(sampleNetscape(false)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ResolveOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(stable) {
		t.Fatalf("got %s", got)
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
