// Package update finds nano-cloud's latest release and fetches it verified.
//
// A release publishes raw binaries, sha256sums.txt, and sha256sums.txt.sig —
// a detached OpenPGP signature by the release key. The key is built into the
// binary (release.asc), the same key install.sh carries, so an update trusts
// exactly what a fresh install trusts and nothing GitHub serves alongside.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is where releases are published.
const Repo = "nanocloud-io/nanocloud"

//go:embed release.asc
var releaseKey []byte

// maxBinaryBytes is the largest release binary Download will write.
const maxBinaryBytes = 512 << 20

// checkEvery bounds how often the daemon asks GitHub, however often the UI
// asks the daemon.
const checkEvery = 12 * time.Hour

// tagPattern reads a version, which a local build may suffix (-dirty).
var tagPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)`)

// releaseTag is all a release's tag may be: it goes into download URLs.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Release is one published release.
type Release struct {
	Tag string `json:"tag"`
	// URL is the release's page, with its notes.
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

// Status is what GET /system/update reports.
type Status struct {
	Current string   `json:"current"`
	Latest  *Release `json:"latest,omitempty"`
	// Available is whether Latest is newer than Current. A build whose
	// version is not a release tag ("dev") is never told to update.
	Available bool      `json:"available"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
	// Disabled means the config turned the check off; nothing is asked.
	Disabled bool `json:"disabled,omitempty"`
}

// Checker asks GitHub for the latest release, at most once per checkEvery.
type Checker struct {
	current string
	client  *http.Client
	api     string
	now     func() time.Time

	mu      sync.Mutex
	latest  *Release
	checked time.Time
	err     error
}

// NewChecker returns a Checker for a binary built as version current.
func NewChecker(current string) *Checker {
	return &Checker{
		current: current,
		client:  &http.Client{Timeout: 15 * time.Second},
		api:     "https://api.github.com/repos/" + Repo + "/releases/latest",
		now:     time.Now,
	}
}

// Status reports the latest release, asking GitHub only when the last answer
// is older than checkEvery. A failed check is remembered as long as a
// successful one, so an offline host does not ask on every page load.
func (c *Checker) Status(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checked.IsZero() || c.now().Sub(c.checked) > checkEvery {
		c.latest, c.err = Latest(ctx, c.client, c.api)
		c.checked = c.now()
	}
	st := Status{Current: c.current, Latest: c.latest, CheckedAt: c.checked}
	if c.err != nil {
		st.Error = c.err.Error()
	}
	if c.latest != nil {
		st.Available = Newer(c.latest.Tag, c.current)
	}
	return st
}

// Latest asks GitHub's API at url for the latest release.
func Latest(ctx context.Context, client *http.Client, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: ask GitHub for the latest release: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("update: read GitHub's answer: %w", err)
	}
	if !releaseTag.MatchString(body.TagName) {
		return nil, fmt.Errorf("update: GitHub named the latest release %q, which is not a version", body.TagName)
	}
	return &Release{Tag: body.TagName, URL: body.HTMLURL, PublishedAt: body.PublishedAt}, nil
}

// Newer reports whether tag is a later release than current. Anything that
// does not start vX.Y.Z — a dev build — is never older than a release.
func Newer(tag, current string) bool {
	a, okA := parse(tag)
	b, okB := parse(current)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	m := tagPattern.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// AssetName is the release binary for this platform.
func AssetName() string {
	return fmt.Sprintf("nanocloud-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// DownloadBase is where releases' files are downloaded from.
const DownloadBase = "https://github.com/" + Repo + "/releases/download"

// Download fetches tag's binary for this platform from base into dir and
// returns its path, having checked that sha256sums.txt is signed by the
// release key and lists the binary's hash. Nothing is returned unverified.
func Download(ctx context.Context, client *http.Client, base, tag, dir string) (string, error) {
	if !releaseTag.MatchString(tag) {
		return "", fmt.Errorf("update: %q is not a release version (vX.Y.Z)", tag)
	}
	url := func(name string) string { return base + "/" + tag + "/" + name }
	sums, err := Fetch(ctx, client, url("sha256sums.txt"), "")
	if errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("update: release %s does not exist", tag)
	}
	if err != nil {
		return "", err
	}
	sig, err := Fetch(ctx, client, url("sha256sums.txt.sig"), "")
	if err != nil {
		return "", err
	}
	if err = VerifySums(sums, sig); err != nil {
		return "", err
	}
	name := AssetName()
	want, err := SumFor(sums, name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "nanocloud")
	if _, err := Fetch(ctx, client, url(name), path); err != nil {
		return "", err
	}
	if err := CheckFile(path, want); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, os.Chmod(path, 0o755)
}

// VerifySums checks sig is the release key's signature over sums.
func VerifySums(sums, sig []byte) error {
	pub, err := parsePublicKey(releaseKey)
	if err != nil {
		return fmt.Errorf("update: read the release key: %w", err)
	}
	if err := verifyDetached(pub, sums, sig); err != nil {
		return fmt.Errorf("update: sha256sums.txt is not signed by the release key: %w", err)
	}
	return nil
}

// SumFor returns the SHA-256 that sums lists for name.
func SumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("update: sha256sums.txt lists no %s — this release has no build for this platform", name)
}

// CheckFile verifies the file at path hashes to want.
func CheckFile(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("update: %s has SHA-256 %s, but the signed sums say %s", path, got, want)
	}
	return nil
}

// ErrNotFound is a release file that does not exist.
var ErrNotFound = errors.New("update: not found")

// Fetch downloads url to path, or into memory when path is empty.
func Fetch(ctx context.Context, client *http.Client, url, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response body
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("update: download %s: %s", url, resp.Status)
	}
	if path == "" {
		return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return nil, err
	}
	// A release binary is tens of megabytes; the cap stops a bad mirror
	// filling the disk before the checksum could refuse it.
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxBinaryBytes)); err != nil {
		f.Close() //nolint:errcheck // already failing
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	return nil, f.Close()
}
