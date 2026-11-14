// Package auth is nano-cloud's single login: one password, browser sessions,
// and named API tokens, kept in auth.json beside metadata.json.
//
// Sessions and tokens are stored only as SHA-256 hashes of their secrets, so
// reading the file does not let anyone sign in; the password is bcrypt. The
// file is 0600 regardless, as services.json is.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nanocloud-io/nanocloud/internal/atomicfile"
)

const (
	// CurrentSchemaVersion is the auth.json format this binary writes.
	CurrentSchemaVersion = 1
	// SessionTTL is how long a browser session lasts without being used.
	SessionTTL = 30 * 24 * time.Hour
	// MinPasswordLength applies to passwords a person chooses.
	MinPasswordLength = 8
	// TokenPrefix marks an API token, so the middleware knows which list to
	// look in and a leaked one is recognisable in a log or a scanner.
	TokenPrefix = "nc_"

	// touchEvery bounds how often using a session or token writes the file.
	touchEvery = time.Hour
	// maxSessions keeps a forgotten browser from growing the file forever.
	maxSessions = 50
)

var (
	// ErrNoPassword means no password has been set: the daemon refuses every
	// protected request until `nanocloud passwd` or `install` sets one.
	ErrNoPassword = errors.New("auth: no password has been set")
	// ErrBadCredentials is a wrong password, or an unknown or expired secret.
	ErrBadCredentials = errors.New("auth: invalid credentials")
	// ErrNotFound is an unknown token ID.
	ErrNotFound = errors.New("auth: no such token")
	// ErrSchemaTooNew means a newer binary wrote auth.json.
	ErrSchemaTooNew = errors.New("auth: file schema is newer than this binary understands")
)

// Kind says how a request authenticated.
type Kind string

const (
	KindSession Kind = "session"
	KindToken   Kind = "token"
)

// Token is an API token as listed to its owner. The secret is shown once, at
// creation, and never stored.
type Token struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

type storedToken struct {
	Token
	Hash string `json:"hash"`
}

type session struct {
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

type state struct {
	SchemaVersion int           `json:"schema_version"`
	PasswordHash  string        `json:"password_hash,omitempty"`
	Sessions      []session     `json:"sessions,omitempty"`
	Tokens        []storedToken `json:"tokens,omitempty"`
}

// Store is auth.json. Reads are served from memory and re-read only when the
// file changes on disk, which is how a password reset by the `nanocloud
// passwd` CLI reaches the running daemon without a restart.
type Store struct {
	f   *atomicfile.File
	now func() time.Time

	mu      sync.Mutex
	cached  *state
	modTime time.Time
	size    int64
}

// New returns the store backed by path. The file need not exist.
func New(path string) *Store {
	return &Store{f: atomicfile.NewPrivate(path), now: time.Now}
}

// Path is the file's location, for messages that need to name it.
func (s *Store) Path() string { return s.f.Path() }

// PasswordSet reports whether a password has been set.
func (s *Store) PasswordSet() (bool, error) {
	st, err := s.load()
	if err != nil {
		return false, err
	}
	return st.PasswordHash != "", nil
}

// SetPassword replaces the password and ends every session. It is the reset
// path, so it does not ask for the old one; tokens are left alone because
// scripts holding them did not leak the password.
func (s *Store) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	return s.update(func(st *state) error {
		st.PasswordHash = string(hash)
		st.Sessions = nil
		return nil
	})
}

// ChangePassword checks the current password, sets a new one, and ends every
// session except keep (the caller's own, which may be empty).
func (s *Store) ChangePassword(current, next, keep string) error {
	if len(next) < MinPasswordLength {
		return fmt.Errorf("auth: the new password must be at least %d characters", MinPasswordLength)
	}
	if err := s.checkPassword(current); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	keepHash := hashSecret(keep)
	return s.update(func(st *state) error {
		st.PasswordHash = string(hash)
		kept := st.Sessions[:0]
		for _, se := range st.Sessions {
			if keep != "" && se.Hash == keepHash {
				kept = append(kept, se)
			}
		}
		st.Sessions = kept
		return nil
	})
}

// Login checks the password and starts a session, returning its secret.
func (s *Store) Login(password string) (string, error) {
	if err := s.checkPassword(password); err != nil {
		return "", err
	}
	secret := randomSecret()
	now := s.now().UTC()
	err := s.update(func(st *state) error {
		st.Sessions = append(liveSessions(st.Sessions, now), session{Hash: hashSecret(secret), CreatedAt: now, LastUsed: now})
		if n := len(st.Sessions); n > maxSessions {
			st.Sessions = st.Sessions[n-maxSessions:]
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// Logout ends the session with this secret. An unknown one is not an error.
func (s *Store) Logout(secret string) error {
	h := hashSecret(secret)
	return s.update(func(st *state) error {
		kept := st.Sessions[:0]
		for _, se := range st.Sessions {
			if se.Hash != h {
				kept = append(kept, se)
			}
		}
		st.Sessions = kept
		return nil
	})
}

// Authenticate checks a session secret or an API token.
func (s *Store) Authenticate(secret string) (Kind, error) {
	st, err := s.load()
	if err != nil {
		return "", err
	}
	if st.PasswordHash == "" {
		return "", ErrNoPassword
	}
	if secret == "" {
		return "", ErrBadCredentials
	}
	h := hashSecret(secret)
	now := s.now().UTC()

	if strings.HasPrefix(secret, TokenPrefix) {
		for _, t := range st.Tokens {
			if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) == 1 {
				if now.Sub(t.LastUsed) > touchEvery {
					s.touch(h, now)
				}
				return KindToken, nil
			}
		}
		return "", ErrBadCredentials
	}
	for _, se := range st.Sessions {
		if subtle.ConstantTimeCompare([]byte(se.Hash), []byte(h)) == 1 {
			if now.Sub(se.LastUsed) > SessionTTL {
				return "", ErrBadCredentials
			}
			if now.Sub(se.LastUsed) > touchEvery {
				s.touch(h, now)
			}
			return KindSession, nil
		}
	}
	return "", ErrBadCredentials
}

// CreateToken makes a named API token and returns its secret, which is not
// stored and cannot be shown again.
func (s *Store) CreateToken(name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", Token{}, errors.New("auth: a token needs a name of 1–64 characters")
	}
	secret := TokenPrefix + randomSecret()
	h := hashSecret(secret)
	t := Token{ID: h[:12], Name: name, CreatedAt: s.now().UTC()}
	err := s.update(func(st *state) error {
		st.Tokens = append(st.Tokens, storedToken{Token: t, Hash: h})
		return nil
	})
	if err != nil {
		return "", Token{}, err
	}
	return secret, t, nil
}

// Tokens lists API tokens, oldest first.
func (s *Store) Tokens() ([]Token, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Token, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		out = append(out, t.Token)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// RevokeToken deletes the token with this ID.
func (s *Store) RevokeToken(id string) error {
	return s.update(func(st *state) error {
		for i, t := range st.Tokens {
			if t.ID == id {
				st.Tokens = append(st.Tokens[:i], st.Tokens[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

// GeneratePassword returns a random password for install and reset to print:
// 20 characters with no look-alikes, so it survives being read off a screen.
func GeneratePassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on any supported platform
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func (s *Store) checkPassword(password string) error {
	st, err := s.load()
	if err != nil {
		return err
	}
	if st.PasswordHash == "" {
		return ErrNoPassword
	}
	if bcrypt.CompareHashAndPassword([]byte(st.PasswordHash), []byte(password)) != nil {
		return ErrBadCredentials
	}
	return nil
}

// touch records that the session or token with this hash was used. A failed
// write costs only accuracy of the last-used time, so it is not reported.
func (s *Store) touch(h string, now time.Time) {
	_ = s.update(func(st *state) error {
		for i := range st.Sessions {
			if st.Sessions[i].Hash == h {
				st.Sessions[i].LastUsed = now
			}
		}
		for i := range st.Tokens {
			if st.Tokens[i].Hash == h {
				st.Tokens[i].LastUsed = now
			}
		}
		return nil
	})
}

// load returns the current state, re-reading the file only when its size or
// modification time has changed since the last read.
func (s *Store) load() (state, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fi, err := os.Stat(s.f.Path())
	switch {
	case os.IsNotExist(err):
		s.cached = &state{SchemaVersion: CurrentSchemaVersion}
		s.modTime, s.size = time.Time{}, -1
		return *s.cached, nil
	case err != nil:
		return state{}, fmt.Errorf("auth: %w", err)
	}
	if s.cached != nil && fi.ModTime().Equal(s.modTime) && fi.Size() == s.size {
		return *s.cached, nil
	}
	data, err := s.f.Read()
	if err != nil {
		return state{}, err
	}
	st, err := decode(data)
	if err != nil {
		return state{}, err
	}
	s.cached, s.modTime, s.size = &st, fi.ModTime(), fi.Size()
	return st, nil
}

func (s *Store) update(fn func(*state) error) error {
	err := s.f.Update(func(current []byte) ([]byte, error) {
		st, err := decode(current)
		if err != nil {
			return nil, err
		}
		if err := fn(&st); err != nil {
			return nil, err
		}
		st.SchemaVersion = CurrentSchemaVersion
		return json.MarshalIndent(st, "", "  ")
	})
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
	return err
}

func decode(data []byte) (state, error) {
	st := state{SchemaVersion: CurrentSchemaVersion}
	if len(data) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, fmt.Errorf("auth: parse auth.json: %w", err)
	}
	if st.SchemaVersion > CurrentSchemaVersion {
		return state{}, ErrSchemaTooNew
	}
	return st, nil
}

func liveSessions(in []session, now time.Time) []session {
	out := in[:0]
	for _, se := range in {
		if now.Sub(se.LastUsed) <= SessionTTL {
			out = append(out, se)
		}
	}
	return out
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on any supported platform
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
