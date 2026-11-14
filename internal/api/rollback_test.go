package api_test

import (
	"fmt"
	"testing"
)

// TestRollbackCreate drives rollbackCreate directly, since reaching it through
// handleVMCreate needs xorriso and qemu-img. When the domain was never
// defined, undefine fails and the files must still go.
func TestRollbackCreate(t *testing.T) {
	for _, defined := range []bool{true, false} {
		t.Run(fmt.Sprintf("defined=%v", defined), func(t *testing.T) {
			m := libvirtManager(t)
			name := fmt.Sprintf("rollback-probe-%v", defined)
			poolDir := t.TempDir()
			disk, iso := seedArtifacts(t, poolDir, name)
			if defined {
				defineProbeDomain(t, m, name, disk, iso)
			}

			specServer(m, nil, poolDir).RollbackCreate(name, disk, iso)

			assertArtifacts(t, false, "after rollbackCreate", disk, iso)
			if exists, err := m.Exists(name); err != nil || exists {
				t.Errorf("after rollbackCreate: Exists = %v, %v; want false, nil", exists, err)
			}
		})
	}
}
