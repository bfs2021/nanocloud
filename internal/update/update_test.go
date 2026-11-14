package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testdata holds v0.1.73's real sha256sums.txt and its signature, so this
// proves the built-in key verifies what the release workflow publishes.
func readFixture(t *testing.T) (sums, sig []byte) {
	t.Helper()
	sums, err := os.ReadFile("testdata/sha256sums.txt")
	if err != nil {
		t.Fatal(err)
	}
	sig, err = os.ReadFile("testdata/sha256sums.txt.sig")
	if err != nil {
		t.Fatal(err)
	}
	return sums, sig
}

func TestVerifySums(t *testing.T) {
	sums, sig := readFixture(t)
	if err := VerifySums(sums, sig); err != nil {
		t.Fatalf("a real release's signature: %v", err)
	}
	tampered := []byte(strings.Replace(string(sums), "8373cb67", "0373cb67", 1))
	if err := VerifySums(tampered, sig); err == nil {
		t.Error("a tampered sha256sums.txt verified")
	}
	if err := VerifySums(sums, []byte("-----BEGIN PGP SIGNATURE-----\n\n=AAAA\n-----END PGP SIGNATURE-----\n")); err == nil {
		t.Error("a garbage signature verified")
	}
}

// install.sh verifies a fresh install with its own copy of the key; an
// update must trust the same key, or one path accepts what the other refuses.
func TestReleaseKeyMatchesInstallScript(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := func(armored string) string {
		var lines []string
		for _, l := range strings.Split(armored, "\n") {
			l = strings.TrimSpace(strings.Trim(l, "'"))
			if l == "" || strings.HasPrefix(l, "-----") || strings.Contains(l, "RELEASE_PUBKEY") {
				continue
			}
			lines = append(lines, l)
		}
		return strings.Join(lines, "\n")
	}
	s := string(script)
	start := strings.Index(s, "-----BEGIN PGP PUBLIC KEY BLOCK-----")
	end := strings.Index(s, "-----END PGP PUBLIC KEY BLOCK-----")
	if start < 0 || end < 0 {
		t.Fatal("install.sh has no public key block")
	}
	if got, want := body(s[start:end]), body(string(releaseKey)); got != want {
		t.Errorf("install.sh's key differs from release.asc:\n%s\n---\n%s", got, want)
	}
}

func TestSumForAndCheckFile(t *testing.T) {
	sums, _ := readFixture(t)
	got, err := SumFor(sums, "nanocloud-linux-arm64")
	if err != nil || got != "5bc496de526838c0ee982d6cd792fdb9d7b532270802c82244461b3fe5ce72cb" {
		t.Errorf("SumFor = %q, %v", got, err)
	}
	if _, err := SumFor(sums, "nanocloud-windows-amd64"); err == nil {
		t.Error("found a sum for a platform the release does not build")
	}

	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const helloSum = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if err := CheckFile(path, helloSum); err != nil {
		t.Errorf("CheckFile on a matching file: %v", err)
	}
	if err := CheckFile(path, strings.Repeat("0", 64)); err == nil {
		t.Error("CheckFile accepted a mismatch")
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		tag, current string
		want         bool
	}{
		{"v0.1.74", "v0.1.73", true},
		{"v0.2.0", "v0.1.99", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.73", "v0.1.73", false},
		{"v0.1.72", "v0.1.73", false},
		{"v0.1.74", "v0.1.73-dirty", true},
		{"v0.1.74", "dev", false},
		{"latest", "v0.1.73", false},
	} {
		if got := Newer(tc.tag, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.tag, tc.current, got, tc.want)
		}
	}
}

func TestCheckerAsksAtMostOncePerInterval(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.80","html_url":"https://example.com/r","published_at":"2026-11-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	now := time.Now()
	c := NewChecker("v0.1.73")
	c.api, c.client, c.now = srv.URL, srv.Client(), func() time.Time { return now }

	st := c.Status(context.Background())
	if !st.Available || st.Latest == nil || st.Latest.Tag != "v0.1.80" || st.Error != "" {
		t.Fatalf("Status = %+v", st)
	}
	c.Status(context.Background())
	if n := calls.Load(); n != 1 {
		t.Errorf("asked GitHub %d times within the interval, want 1", n)
	}
	now = now.Add(checkEvery + time.Minute)
	c.Status(context.Background())
	if n := calls.Load(); n != 2 {
		t.Errorf("asked GitHub %d times after the interval, want 2", n)
	}
}

func TestCheckerReportsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewChecker("v0.1.73")
	c.api, c.client = srv.URL, srv.Client()
	if st := c.Status(context.Background()); st.Available || st.Error == "" {
		t.Errorf("Status after a failed check = %+v, want an error and nothing available", st)
	}
}

// releaseServer serves v0.1.73's real signed sums, and body as every binary.
func releaseServer(t *testing.T, sig []byte, body string) *httptest.Server {
	t.Helper()
	sums, realSig := readFixture(t)
	if sig == nil {
		sig = realSig
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case !strings.HasPrefix(r.URL.Path, "/v0.1.73/"):
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/sha256sums.txt"):
			_, _ = w.Write(sums)
		case strings.HasSuffix(r.URL.Path, "/sha256sums.txt.sig"):
			_, _ = w.Write(sig)
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The happy path needs a real 18 MB release binary; TestVerifySums and
// TestSumForAndCheckFile cover its two checks. These are the refusals.
func TestDownloadRefusesAnythingUnverified(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		srv  *httptest.Server
		tag  string
		says string
	}{
		"no such release":      {releaseServer(t, nil, ""), "v9.9.9", "does not exist"},
		"not a version":        {releaseServer(t, nil, ""), "../x", "not a release version"},
		"version, then a path": {releaseServer(t, nil, ""), "v0.1.73/../../x", "not a release version"},
		"bad signature":        {releaseServer(t, []byte("not a signature"), ""), "v0.1.73", "not signed"},
		"tampered binary":      {releaseServer(t, nil, "evil"), "v0.1.73", "SHA-256"},
	} {
		dir := t.TempDir()
		path, err := Download(ctx, tc.srv.Client(), tc.srv.URL, tc.tag, dir)
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: Download = %q, %v; want an error saying %q", name, path, err, tc.says)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("%s: left %d file(s) behind", name, len(left))
		}
	}
}
