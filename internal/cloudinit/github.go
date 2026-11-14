package cloudinit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The daemon fetches keys instead of using cloud-init's ssh_import_id, which
// needs the ssh-import-id package (missing on Alpine and minimal RHEL-likes)
// and needs the guest to reach github.com on first boot.

// ErrNoGitHubKeys means the account exists but has published no keys, which
// retrying will not fix.
var ErrNoGitHubKeys = errors.New("cloudinit: that GitHub account has no public SSH keys")

// githubCharset is a GitHub login's alphabet and length; the hyphen rules are
// in validGitHubUsername, since RE2 has no lookahead.
var githubCharset = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)

func validGitHubUsername(s string) bool {
	return githubCharset.MatchString(s) &&
		!strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-") &&
		!strings.Contains(s, "--")
}

// githubKeysBase is a var so tests can point it at a local server.
var githubKeysBase = "https://github.com"

// githubKeysTimeout bounds the fetch, which sits on the VM create path.
const githubKeysTimeout = 8 * time.Second

// maxGitHubKeyBytes caps the response, so a redirect to something huge is not
// read into memory.
const maxGitHubKeyBytes = 64 << 10

// FetchGitHubKeys returns the public SSH keys a GitHub account publishes.
// Failures are returned, since a VM created without its keys may be one
// nobody can log into.
func FetchGitHubKeys(ctx context.Context, username string) ([]string, error) {
	username = strings.TrimSpace(username)
	if !validGitHubUsername(username) {
		return nil, fmt.Errorf("cloudinit: %q is not a GitHub username", username)
	}

	ctx, cancel := context.WithTimeout(ctx, githubKeysTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubKeysBase+"/"+username+".keys", nil)
	if err != nil {
		return nil, fmt.Errorf("cloudinit: build GitHub keys request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudinit: fetch keys for %q from GitHub: %w", username, err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on a read-only GET

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("cloudinit: GitHub has no account named %q", username)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloudinit: GitHub answered %s for %q's keys", resp.Status, username)
	}

	var keys []string
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxGitHubKeyBytes))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			keys = append(keys, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cloudinit: read %q's keys from GitHub: %w", username, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w (%s)", ErrNoGitHubKeys, username)
	}
	return keys, nil
}
