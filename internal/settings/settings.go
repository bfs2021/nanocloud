// Package settings holds the options changed from the UI while the daemon
// runs. They live in settings.json beside metadata.json, because config.yaml
// is the operator's file and read-only under the systemd sandbox. A value set
// here overrides config.yaml; an unset one defers to it.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/nanocloud-io/nanocloud/internal/atomicfile"
	"github.com/nanocloud-io/nanocloud/internal/config"
)

// CurrentSchemaVersion is the settings.json format this binary writes.
const CurrentSchemaVersion = 1

// ErrSchemaTooNew means a newer binary wrote settings.json; writing it from
// this one would drop the settings it does not know.
var ErrSchemaTooNew = errors.New("settings: file schema is newer than this binary understands")

// Pool is a storage pool added from the UI.
type Pool struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Settings is the content of settings.json. Every field is optional.
type Settings struct {
	SchemaVersion int `json:"schema_version"`
	// SelfHealing turns the reconcile loop on or off; nil defers to
	// config.yaml's reconcile.enabled.
	SelfHealing *bool `json:"self_healing,omitempty"`
	// DefaultPool names the pool the wizard preselects; empty defers to the
	// pool config.yaml marks default.
	DefaultPool string `json:"default_pool,omitempty"`
	Pools       []Pool `json:"pools,omitempty"`
	// UpdateCheck lets the daemon ask GitHub for new releases; nil defers to
	// config.yaml's update_check.
	UpdateCheck *bool `json:"update_check,omitempty"`
}

// UpdateCheckEnabled is whether the daemon may look for new releases.
func (s Settings) UpdateCheckEnabled(cfg *config.Config) bool {
	if s.UpdateCheck != nil {
		return *s.UpdateCheck
	}
	return cfg.UpdateCheckEnabled()
}

// SelfHealingEnabled is whether the reconcile loop should run.
func (s Settings) SelfHealingEnabled(cfg *config.Config) bool {
	if s.SelfHealing != nil {
		return *s.SelfHealing
	}
	return cfg.ReconcileEnabled()
}

// StoragePools is config.yaml's pools followed by the ones added here, with
// exactly one marked default: DefaultPool when it names a pool, else the one
// config.yaml marks.
func (s Settings) StoragePools(cfg *config.Config) []config.StoragePool {
	pools := append([]config.StoragePool(nil), cfg.StoragePools...)
	for _, p := range s.Pools {
		pools = append(pools, config.StoragePool{Name: p.Name, Path: p.Path})
	}
	if s.DefaultPool == "" {
		return pools
	}
	found := false
	for _, p := range pools {
		found = found || p.Name == s.DefaultPool
	}
	if found {
		for i := range pools {
			pools[i].Default = pools[i].Name == s.DefaultPool
		}
	}
	return pools
}

// Store reads settings.json once and keeps it in memory: the daemon is its
// only writer.
type Store struct {
	file *atomicfile.File
	mu   sync.Mutex
	cur  Settings
}

// Open reads the settings at path. A missing file is no settings.
func Open(path string) (*Store, error) {
	s := &Store{file: atomicfile.New(path)}
	data, err := s.file.Read()
	if err != nil {
		return nil, err
	}
	if s.cur, err = decode(data); err != nil {
		return nil, err
	}
	return s, nil
}

// PathFor is settings.json beside the metadata file at metadataPath.
func PathFor(metadataPath string) string {
	return filepath.Join(filepath.Dir(metadataPath), "settings.json")
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cur)
}

// Update applies fn to a copy of the settings and saves the result; if fn
// or the write fails, nothing changes.
func (s *Store) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cur)
	if err := fn(&next); err != nil {
		return s.cur, err
	}
	next.SchemaVersion = CurrentSchemaVersion
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return s.cur, err
	}
	if err = s.file.Update(func([]byte) ([]byte, error) { return append(data, '\n'), nil }); err != nil {
		return s.cur, fmt.Errorf("settings: save: %w", err)
	}
	s.cur = next
	return clone(next), nil
}

func decode(data []byte) (Settings, error) {
	var st Settings
	if len(data) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("settings: parse: %w", err)
	}
	if st.SchemaVersion > CurrentSchemaVersion {
		return st, fmt.Errorf("%w (version %d)", ErrSchemaTooNew, st.SchemaVersion)
	}
	return st, nil
}

func clone(s Settings) Settings {
	s.Pools = append([]Pool(nil), s.Pools...)
	for _, b := range []**bool{&s.SelfHealing, &s.UpdateCheck} {
		if *b != nil {
			v := **b
			*b = &v
		}
	}
	return s
}
