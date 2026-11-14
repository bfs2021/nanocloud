package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := New(filepath.Join(t.TempDir(), "auth.json"))
	if err := s.SetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNoPasswordRefusesEverything(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "auth.json"))
	if set, err := s.PasswordSet(); err != nil || set {
		t.Fatalf("PasswordSet = %v, %v on a fresh store", set, err)
	}
	if _, err := s.Login(""); !errors.Is(err, ErrNoPassword) {
		t.Errorf("Login with no password set: %v, want ErrNoPassword", err)
	}
	if _, err := s.Authenticate("anything"); !errors.Is(err, ErrNoPassword) {
		t.Errorf("Authenticate with no password set: %v, want ErrNoPassword", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newStore(t)
	if _, err := s.Login("wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	secret, err := s.Login("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if k, err := s.Authenticate(secret); err != nil || k != KindSession {
		t.Fatalf("Authenticate(session) = %q, %v", k, err)
	}
	if _, err := s.Authenticate(secret + "x"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("a different secret authenticated: %v", err)
	}

	// Sliding expiry: unused for longer than the TTL, the session is gone.
	s.now = func() time.Time { return time.Now().Add(SessionTTL + 2*time.Hour) }
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("expired session authenticated: %v", err)
	}
	s.now = time.Now

	if err := s.Logout(secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("logged-out session authenticated: %v", err)
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	s := newStore(t)
	mine, _ := s.Login("correct horse")
	other, _ := s.Login("correct horse")

	if err := s.ChangePassword("wrong", "battery staple", mine); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("change with the wrong current password: %v", err)
	}
	if err := s.ChangePassword("correct horse", "short", mine); err == nil {
		t.Fatal("accepted a password shorter than the minimum")
	}
	if err := s.ChangePassword("correct horse", "battery staple", mine); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(mine); err != nil {
		t.Errorf("the caller's own session ended: %v", err)
	}
	if _, err := s.Authenticate(other); err == nil {
		t.Error("another session survived a password change")
	}
	if _, err := s.Login("battery staple"); err != nil {
		t.Errorf("new password: %v", err)
	}

	// A reset ends every session.
	if err := s.SetPassword("reset by root"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(mine); err == nil {
		t.Error("a session survived a password reset")
	}
}

func TestTokens(t *testing.T) {
	s := newStore(t)
	secret, tok, err := s.CreateToken("ci")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) || tok.ID == "" || tok.Name != "ci" {
		t.Fatalf("CreateToken = %q, %+v", secret, tok)
	}
	if k, err := s.Authenticate(secret); err != nil || k != KindToken {
		t.Fatalf("Authenticate(token) = %q, %v", k, err)
	}
	// A reset leaves tokens alone: the scripts holding them leaked nothing.
	if err := s.SetPassword("reset by root"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); err != nil {
		t.Errorf("token stopped working after a password reset: %v", err)
	}

	list, _ := s.Tokens()
	if len(list) != 1 || list[0].ID != tok.ID || list[0].LastUsed.IsZero() {
		t.Errorf("Tokens = %+v, want one used token", list)
	}
	if err := s.RevokeToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("revoked token authenticated: %v", err)
	}
	if err := s.RevokeToken(tok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking twice: %v, want ErrNotFound", err)
	}
}

func TestFileHoldsNoSecrets(t *testing.T) {
	s := newStore(t)
	session, _ := s.Login("correct horse")
	token, _, _ := s.CreateToken("ci")

	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"correct horse", session, token} {
		if strings.Contains(string(data), secret) {
			t.Errorf("auth.json contains a secret in the clear: %q", secret)
		}
	}
	if fi, _ := os.Stat(s.Path()); fi.Mode().Perm() != 0o600 {
		t.Errorf("auth.json mode = %v, want 0600", fi.Mode().Perm())
	}
}

// The CLI resets the password from another process; the daemon must see it
// without a restart.
func TestAnotherWriterIsSeen(t *testing.T) {
	daemon := newStore(t)
	session, _ := daemon.Login("correct horse")

	cli := New(daemon.Path())
	time.Sleep(10 * time.Millisecond) // a distinct mtime on coarse file systems
	if err := cli.SetPassword("reset by root"); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Authenticate(session); err == nil {
		t.Error("the daemon kept a session the CLI ended")
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path).PasswordSet(); !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("PasswordSet = %v, want ErrSchemaTooNew", err)
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }

	for range freeFailures {
		if d := l.Attempt("a"); d != 0 {
			t.Fatalf("waiting %v within the free failures", d)
		}
	}
	l.Attempt("a")
	if d := l.Attempt("a"); d != 2*time.Second {
		t.Errorf("after the first paid failure: wait %v, want 2s", d)
	}
	if d := l.Attempt("b"); d != 0 {
		t.Errorf("another client is held back by a's failures: %v", d)
	}
	for range 40 {
		now = now.Add(maxLockout)
		l.Attempt("a")
	}
	if d := l.Attempt("a"); d != maxLockout {
		t.Errorf("wait after many failures = %v, want the %v cap", d, maxLockout)
	}
	l.Succeed("a")
	if d := l.Attempt("a"); d != 0 {
		t.Errorf("success did not clear the wait: %v", d)
	}
}

func TestLimiterCountsConcurrentAttempts(t *testing.T) {
	l := NewLimiter()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			if l.Attempt("a") == 0 {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != freeFailures+1 {
		t.Errorf("%d of 200 concurrent attempts were let through, want %d", n, freeFailures+1)
	}
}
