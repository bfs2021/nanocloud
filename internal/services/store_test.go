package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_CreateGetUpdateDelete(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "services.json"))

	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %v, %v", list, err)
	}
	inst := Instance{Name: "db", Recipe: "postgres", State: StatePulling, Params: map[string]string{"password": "secret"}}
	if err := s.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(inst); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v, want ErrExists", err)
	}
	if err := s.Update("db", func(i *Instance) { i.State = StateReady }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("db")
	if err != nil || got.State != StateReady || got.Params["password"] != "secret" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := s.Update("nope", func(*Instance) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	if err := s.Delete("db"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("db"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := s.Delete("db"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// The file holds database passwords and S3 keys.
func TestStore_FileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	s := NewStore(path)
	if err := s.Create(Instance{Name: "db"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("services.json mode = %o, want 600", mode)
	}
}

func TestStore_RefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(path, []byte(`{"schema_version": 99, "services": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if _, err := s.List(); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("list: %v, want ErrSchemaTooNew", err)
	}
	if err := s.Create(Instance{Name: "x"}); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("create: %v, want ErrSchemaTooNew", err)
	}
}
