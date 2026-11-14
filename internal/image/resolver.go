package image

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// httpDoer is satisfied by *http.Client and by test fakes.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ChecksumType names the hash algorithm recorded in a resolved image.
type ChecksumType string

const (
	ChecksumSHA256 ChecksumType = "sha256"
	ChecksumSHA512 ChecksumType = "sha512"
)

// ResolvedImage holds everything the puller needs to download and verify one
// cloud image.
type ResolvedImage struct {
	// ImageURL is the direct download URL for the image file.
	ImageURL string
	// ChecksumURL is the URL of the file that contains (or is) the checksum.
	ChecksumURL string
	// ChecksumType identifies which hash algorithm ChecksumURL encodes.
	ChecksumType ChecksumType
	// Filename is the bare filename (no path) of the image.
	Filename string
	// ApproxBytes is a rough size estimate used for download-progress reporting.
	ApproxBytes int64
}

// DirIndexResolver resolves a CatalogEntry with an IndexURL to a concrete
// image at pull time, by reading the server's autoindex page, picking the
// newest matching file and locating its checksum. Use NewDirIndexResolver.
type DirIndexResolver struct {
	client httpDoer
}

// NewDirIndexResolver returns a resolver that uses client for HTTP requests.
func NewDirIndexResolver(client httpDoer) *DirIndexResolver {
	return &DirIndexResolver{client: client}
}

// Resolve returns the image and checksum to download for entry. A static entry
// is returned as is.
func (r *DirIndexResolver) Resolve(ctx context.Context, entry CatalogEntry) (ResolvedImage, error) {
	if entry.IndexURL == "" {
		ct := ChecksumSHA256
		if entry.ChecksumType == string(ChecksumSHA512) {
			ct = ChecksumSHA512
		}
		return ResolvedImage{
			ImageURL:     entry.URL,
			ChecksumURL:  entry.ChecksumURL,
			ChecksumType: ct,
			Filename:     entry.Filename,
			ApproxBytes:  entry.ApproxBytes,
		}, nil
	}

	body, err := r.getBody(ctx, entry.IndexURL)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolver: fetch index %s: %w", entry.IndexURL, err)
	}

	links := ParseIndexLinks(body)
	if len(links) == 0 {
		return ResolvedImage{}, fmt.Errorf("resolver: no links found in index %s", entry.IndexURL)
	}

	pat, err := regexp.Compile(entry.FilePattern)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolver: invalid FilePattern %q: %w", entry.FilePattern, err)
	}

	var candidates []string
	for _, link := range links {
		if pat.MatchString(link) {
			candidates = append(candidates, link)
		}
	}
	if len(candidates) == 0 {
		return ResolvedImage{}, fmt.Errorf("resolver: no file matched pattern %q in index %s", entry.FilePattern, entry.IndexURL)
	}

	// Natural order, so "3.20.10" is newer than "3.20.9".
	filename := candidates[0]
	for _, c := range candidates[1:] {
		if naturalGreater(c, filename) {
			filename = c
		}
	}

	baseURL := strings.TrimRight(entry.IndexURL, "/")
	imageURL := baseURL + "/" + filename

	checksumURL, checksumType, err := r.locateChecksum(ctx, baseURL, filename, links)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolver: no checksum for %q in %s: %w", filename, entry.IndexURL, err)
	}

	return ResolvedImage{
		ImageURL:     imageURL,
		ChecksumURL:  checksumURL,
		ChecksumType: checksumType,
		Filename:     filename,
		ApproxBytes:  entry.ApproxBytes,
	}, nil
}

// locateChecksum picks, in order: a <filename>.sha512 or .sha256 sidecar,
// SHA512SUMS, SHA256SUMS, then any *-CHECKSUM file. It looks only at links
// already scraped from the index.
func (r *DirIndexResolver) locateChecksum(ctx context.Context, baseURL, filename string, links []string) (string, ChecksumType, error) {
	linkSet := make(map[string]bool, len(links))
	for _, l := range links {
		linkSet[l] = true
	}

	if sidecar := filename + ".sha512"; linkSet[sidecar] {
		return baseURL + "/" + sidecar, ChecksumSHA512, nil
	}

	if sidecar := filename + ".sha256"; linkSet[sidecar] {
		return baseURL + "/" + sidecar, ChecksumSHA256, nil
	}

	combined := []struct {
		name string
		kind ChecksumType
	}{
		{"SHA512SUMS", ChecksumSHA512},
		{"SHA256SUMS", ChecksumSHA256},
	}
	for _, c := range combined {
		if linkSet[c.name] {
			return baseURL + "/" + c.name, c.kind, nil
		}
	}

	// Fedora, Rocky and Alma publish SHA256 *-CHECKSUM files.
	for _, l := range links {
		if strings.HasSuffix(strings.ToUpper(l), "-CHECKSUM") {
			return baseURL + "/" + l, ChecksumSHA256, nil
		}
	}

	return "", "", fmt.Errorf("no checksum sidecar or combined sums file found")
}

// getBody fetches url, treating any status but 200 as an error.
func (r *DirIndexResolver) getBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort body cleanup

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// ParseIndexLinks returns the file links on an Apache/Nginx autoindex page, in
// document order. It skips absolute paths and URLs, query-only links and
// subdirectories. A regexp is enough for autoindex pages and avoids an HTML
// parser dependency.
func ParseIndexLinks(body []byte) []string {
	re := regexp.MustCompile(`(?i)<a\s+[^>]*href="([^"#?][^"]*)"`)
	matches := re.FindAllSubmatch(body, -1)

	var links []string
	for _, m := range matches {
		href := string(m[1])
		if strings.HasPrefix(href, "/") ||
			strings.HasPrefix(href, "http://") ||
			strings.HasPrefix(href, "https://") ||
			strings.HasSuffix(href, "/") {
			continue
		}
		links = append(links, href)
	}
	return links
}

// naturalGreater reports whether a sorts after b when runs of digits compare
// by numeric value and everything else byte by byte.
func naturalGreater(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ad, bd := isDigit(a[i]), isDigit(b[j])
		if ad && bd {
			si, sj := i, j
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) > len(nb)
			}
			if na != nb {
				return na > nb
			}
			continue
		}
		if a[i] != b[j] {
			return a[i] > b[j]
		}
		i++
		j++
	}
	return len(a)-i > len(b)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
