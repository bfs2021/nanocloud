package api

import (
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/reconcile"
)

// TestVMPhase covers the states a running VM passes through before it is
// usable. Past the grace period a VM with no address is "no-network", not
// "booting", and an unconfirmed address, such as a lease that outlived its
// guest, counts for no more than none.
func TestVMPhase(t *testing.T) {
	grace := int64(noIPGracePeriod.Seconds())
	const ip = "192.168.1.51"

	for _, tc := range []struct {
		name      string
		status    string
		ip        string
		confirmed bool
		uptime    int64
		health    reconcile.VMHealth
		want      string
	}{
		{"stopped", "shut off", "", false, 0, reconcile.VMHealth{}, "stopped"},
		{"stopped VMs never report a network fault", "shut off", "", false, grace * 10, reconcile.VMHealth{}, "stopped"},
		{"just started, no IP yet", "running", "", false, 5, reconcile.VMHealth{}, "booting"},
		{"still within the grace period", "running", "", false, grace - 1, reconcile.VMHealth{}, "booting"},
		{"no IP well past the grace period", "running", "", false, grace + 1, reconcile.VMHealth{}, "no-network"},
		{"has a confirmed IP", "running", ip, true, 5, reconcile.VMHealth{}, "ready"},
		{"a confirmed IP outranks a long uptime", "running", ip, true, grace * 10, reconcile.VMHealth{}, "ready"},
		{"gave up -> gave-up, not stopped", "shut off", "", false, 0, reconcile.VMHealth{GaveUp: true}, "gave-up"},
		{"backing off -> crash-looping", "shut off", "", false, 0, reconcile.VMHealth{Attempts: 2}, "crash-looping"},
		{
			"running live outranks a stale gaveUp from before a manual restart",
			"running", ip, true, 5, reconcile.VMHealth{GaveUp: true, Attempts: 6}, "ready",
		},
		// An unconfirmed address counts for no more than no address.
		{"an unconfirmed IP is still booting inside the grace period", "running", ip, false, 5, reconcile.VMHealth{}, "booting"},
		{"an unconfirmed IP past the grace period is no-network", "running", ip, false, grace + 1, reconcile.VMHealth{}, "no-network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := vmPhase(tc.status, tc.ip, tc.confirmed, tc.uptime, tc.health)
			if got != tc.want {
				t.Errorf("vmPhase(%q, %q, %v, %d, %+v) = %q, want %q",
					tc.status, tc.ip, tc.confirmed, tc.uptime, tc.health, got, tc.want)
			}
		})
	}
}
