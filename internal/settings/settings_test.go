package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func testConfig() *config.Config {
	return &config.Config{StoragePools: []config.StoragePool{
		{Name: "default", Path: "/var/lib/nano-cloud/vm-disks", Default: true},
		{Name: "fast", Path: "/mnt/nvme"},
	}}
}

func defaults(pools []config.StoragePool) []string {
	var out []string
	for _, p := range pools {
		if p.Default {
			out = append(out, p.Name)
		}
	}
	return out
}

func TestStoragePoolsOverlay(t *testing.T) {
	cfg := testConfig()
	for _, tc := range []struct {
		name string
		st   Settings
		want []string // the default pool(s)
		n    int
	}{
		{"nothing set", Settings{}, []string{"default"}, 2},
		{"default moved to a config pool", Settings{DefaultPool: "fast"}, []string{"fast"}, 2},
		{"default moved to an added pool", Settings{DefaultPool: "big", Pools: []Pool{{Name: "big", Path: "/srv/big"}}}, []string{"big"}, 3},
		// A default naming a pool since removed from config.yaml must not
		// leave the host with no default at all.
		{"default names a pool that is gone", Settings{DefaultPool: "gone"}, []string{"default"}, 2},
	} {
		pools := tc.st.StoragePools(cfg)
		if got := defaults(pools); len(pools) != tc.n || len(got) != 1 || got[0] != tc.want[0] {
			t.Errorf("%s: %d pools, default %v; want %d, %v", tc.name, len(pools), got, tc.n, tc.want)
		}
	}
	if cfg.StoragePools[1].Default {
		t.Error("the overlay changed config.yaml's own pools")
	}
}

func TestSelfHealingDefersToConfigUntilSet(t *testing.T) {
	cfg := testConfig()
	if !(Settings{}).SelfHealingEnabled(cfg) {
		t.Error("unset in both: want on")
	}
	cfg.Reconcile.Enabled = boolPtr(false)
	if (Settings{}).SelfHealingEnabled(cfg) {
		t.Error("unset here, off in config.yaml: want off")
	}
	if !(Settings{SelfHealing: boolPtr(true)}).SelfHealingEnabled(cfg) {
		t.Error("on here, off in config.yaml: want on — a setting overrides the file")
	}
}

func TestStoreKeepsWhatItSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(func(st *Settings) error {
		st.SelfHealing, st.DefaultPool = boolPtr(false), "big"
		st.Pools = []Pool{{Name: "big", Path: "/srv/big"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	if _, err = s.Update(func(st *Settings) error { st.DefaultPool = "other"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("Update = %v, want the function's error", err)
	}
	if got := s.Get().DefaultPool; got != "big" {
		t.Errorf("a failed update changed DefaultPool to %q", got)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Get()
	if got.SelfHealing == nil || *got.SelfHealing || got.DefaultPool != "big" || len(got.Pools) != 1 || got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("reopened = %+v", got)
	}

	// Get hands out a copy: changing it must not change the store.
	got.Pools[0].Name = "mutated"
	if reopened.Get().Pools[0].Name != "big" {
		t.Error("Get returned the store's own slice")
	}
}

func TestOpenRefusesANewerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"schema_version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("Open = %v, want ErrSchemaTooNew", err)
	}
}
