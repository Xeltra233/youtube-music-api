package cookies

import (
	"fmt"
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

func TestSnapshotForYtdlpDoesNotTouchStable(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	body := sampleNetscape(true)
	if err := os.WriteFile(stable, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	snap, cleanup, err := SnapshotForYtdlp(stable)
	if err != nil {
		t.Fatal(err)
	}
	if snap == "" || snap == stable {
		t.Fatalf("expected temp snapshot, got %q", snap)
	}
	if !strings.HasSuffix(snap, ".tmp") {
		t.Fatalf("snapshot must not look like a promotable drop-in: %q", snap)
	}
	// Simulate yt-dlp rewriting the cookie file to anonymous visitor jar.
	anon := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tFALSE\t0\tPREF\thl=en\n"
	if err := os.WriteFile(snap, []byte(anon), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CommitSnapshotIfBetter(snap, stable); err != nil {
		t.Fatal(err)
	}
	cleanup()
	after, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("stable login jar must not be overwritten by anonymous snapshot")
	}
	if FileExistsNonEmpty(snap) {
		t.Fatal("snapshot temp should be cleaned")
	}
}

func TestCommitSnapshotAllowsUpgradeFromAnonymous(t *testing.T) {
	dir := t.TempDir()
	stable := filepath.Join(dir, StableFileName)
	anon := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tFALSE\t0\tPREF\thl=en\n"
	login := sampleNetscape(true)
	if err := os.WriteFile(stable, []byte(anon), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp.txt")
	if err := os.WriteFile(tmp, []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CommitSnapshotIfBetter(tmp, stable); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "LOGIN_INFO") {
		t.Fatalf("anonymous stable should upgrade to login jar, got %q", got)
	}
}

func TestInspectCookieFileParsesHttpOnlyLoginCookie(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "http-only.txt")
	body := "# Netscape HTTP Cookie File\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\thttp-only-login\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	quality, err := inspectCookieFile(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !quality.Valid || !quality.LoggedIn || quality.AuthCookies != 1 {
		t.Fatalf("unexpected quality: %+v", quality)
	}
	header, err := HeaderFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(header, "LOGIN_INFO=http-only-login") {
		t.Fatalf("HttpOnly cookie missing from header: %q", header)
	}
}

func TestInspectCookieFileDoesNotTreatSIDAloneAsLoggedIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sid-only.txt")
	body := "# Netscape HTTP Cookie File\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSID\tsid-only\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	quality, err := inspectCookieFile(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !quality.Valid || quality.LoggedIn {
		t.Fatalf("SID alone must remain anonymous: %+v", quality)
	}
}

func TestInspectCookieFileIgnoresExpiredAuthenticationCookies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expired.txt")
	now := time.Now()
	expired := now.Add(-time.Hour).Unix()
	body := fmt.Sprintf("# Netscape HTTP Cookie File\n"+
		".youtube.com\tTRUE\t/\tFALSE\t0\tVISITOR_INFO1_LIVE\tvisitor\n"+
		".youtube.com\tTRUE\t/\tTRUE\t%d\tLOGIN_INFO\texpired-login\n"+
		".google.com\tTRUE\t/\tTRUE\t%d\tSID\texpired-sid\n"+
		".google.com\tTRUE\t/\tTRUE\t%d\tSAPISID\texpired-sapisid\n", expired, expired, expired)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	quality, err := inspectCookieFile(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if !quality.Valid || quality.LoggedIn || quality.AuthCookies != 0 || quality.CookieCount != 1 {
		t.Fatalf("expired authentication cookies must be ignored: %+v", quality)
	}
	header, err := HeaderFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(header, "expired-") {
		t.Fatalf("expired cookies leaked into header: %q", header)
	}
}

func TestCookieDomainMatchingRejectsSuffixLookalikes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lookalike.txt")
	body := "# Netscape HTTP Cookie File\n" +
		".evilgoogle.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tevil-login\n" +
		".youtube.com\tTRUE\t/\tFALSE\t0\tVISITOR_INFO1_LIVE\tvisitor\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	quality, err := inspectCookieFile(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if quality.LoggedIn || quality.AuthCookies != 0 || quality.YouTubeGoogleCookies != 1 {
		t.Fatalf("lookalike domain influenced quality: %+v", quality)
	}
	header, err := HeaderFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(header, "evil-login") || strings.Contains(header, "LOGIN_INFO") {
		t.Fatalf("lookalike domain leaked into header: %q", header)
	}
}

func TestWriteFileAtomicReplacesWithoutTemporaryResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StableFileName)
	for i := 0; i < 5; i++ {
		want := []byte(fmt.Sprintf("replacement-%d", i))
		if err := writeFileAtomic(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("replacement %d: got %q want %q", i, got, want)
		}
	}
	for _, pattern := range []string{"." + StableFileName + ".tmp-*", "." + StableFileName + ".bak-*"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("atomic write left residue for %q: %v", pattern, matches)
		}
	}
}

func TestWriteFileAtomicDoesNotReplaceDirectory(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(destination, []byte("cookie data"), 0o600); err == nil {
		t.Fatal("directory destination should be rejected")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("directory destination was replaced")
	}
}
