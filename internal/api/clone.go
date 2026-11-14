package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// cloneRequest is the body of POST /vms/{id}/clone.
type cloneRequest struct {
	Name string `json:"name"`
	// Start sets the clone's power state; the source is always stopped.
	Start bool `json:"start"`
}

// handleVMClone handles POST /vms/{id}/clone. Every disk is flattened into a
// file of its own, so the clone survives its source's deletion. That can take
// minutes, and the request stays synchronous so a failure has someone to
// report to.
func (s *Server) handleVMClone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	source, ok := s.specEntry(w, id)
	if !ok {
		return
	}

	var req cloneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !vmNamePattern.MatchString(req.Name) {
		http.Error(w, vmNameRule, http.StatusBadRequest)
		return
	}
	if source.PoolPath == "" {
		http.Error(w, fmt.Sprintf("%q has no storage pool recorded, so there is nowhere to copy its disks to", id), http.StatusUnprocessableEntity)
		return
	}
	if exists, err := s.vmManager.Exists(req.Name); err != nil {
		writeVMManagerError(w, fmt.Sprintf("check vm %q", req.Name), err)
		return
	} else if exists {
		http.Error(w, fmt.Sprintf("a VM named %q already exists", req.Name), http.StatusConflict)
		return
	}

	// As for a resize, qemu-img needs the source off to read a consistent
	// image.
	v, err := s.vmManager.Get(id)
	if err != nil {
		writeVMManagerError(w, fmt.Sprintf("check whether %q is stopped before cloning it", id), err)
		return
	}
	if v.State != "stopped" {
		http.Error(w, fmt.Sprintf("%q is %s — stop it before cloning, because its disks cannot be copied consistently while QEMU has them open", id, v.State), http.StatusConflict)
		return
	}

	diskPath := storage.DiskPathFor(source.PoolPath, req.Name)
	isoPath := storage.ISOPathFor(source.PoolPath, req.Name)
	if _, statErr := os.Stat(diskPath); statErr == nil {
		http.Error(w, fmt.Sprintf("a disk for %q already exists at %s — remove it before reusing the name", req.Name, diskPath), http.StatusConflict)
		return
	}

	clone, warnings := cloneEntry(source, req.Name, req.Start)

	// The clone inherits the source's VLAN. Ensure it before the slow copies.
	if err := s.ensureVLAN(clone); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Appended before each file is written, so a copy that dies partway is
	// still cleaned up.
	var created []string
	cleanup := func() {
		for _, p := range created {
			os.Remove(p) //nolint:errcheck // best-effort; an error response is already going out
		}
	}

	// The seed first: it is cheap, and a missing xorriso should show up before
	// the disk copies.
	created = append(created, isoPath)
	if err := s.buildCloneSeed(source, req.Name, isoPath); err != nil {
		cleanup()
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	created = append(created, diskPath)
	if err := storage.CopyDisk(storage.DiskPathFor(source.PoolPath, id), diskPath); err != nil {
		cleanup()
		http.Error(w, fmt.Sprintf("copy %q's boot disk: %v", id, err), http.StatusInternalServerError)
		return
	}
	// Every attached disk too, or both domains would write the source's files.
	for _, d := range source.Disks {
		dest, pathErr := freeDiskPath(source.PoolPath, req.Name, d.Target)
		if pathErr != nil {
			cleanup()
			http.Error(w, pathErr.Error(), http.StatusConflict)
			return
		}
		created = append(created, dest)
		if err := storage.CopyDisk(d.Path, dest); err != nil {
			cleanup()
			http.Error(w, fmt.Sprintf("copy %q's disk %s: %v", id, d.Target, err), http.StatusInternalServerError)
			return
		}
		copied := d
		copied.Path = dest
		clone.Disks = append(clone.Disks, copied)
	}

	if err := s.vmManager.DefineFromSpec(clone, diskPath, isoPath); err != nil {
		cleanup()
		if errors.Is(err, vm.ErrIncompleteSpec) {
			// An entry without vcpus or memory cannot be built; retrying won't help.
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeVMManagerError(w, fmt.Sprintf("define clone %q", req.Name), err)
		return
	}
	if req.Start {
		if err := s.vmManager.Start(req.Name); err != nil {
			s.rollbackCreate(req.Name, created...)
			writeVMManagerError(w, fmt.Sprintf("start clone %q", req.Name), err)
			return
		}
	}

	// Last, as at create: a failure here rolls the whole clone back, since a
	// domain with no entry would be lost on the next reboot.
	if err := s.metaStore.Upsert(req.Name, func(e *metadata.VMEntry) { *e = clone }); err != nil {
		s.rollbackCreate(req.Name, created...)
		http.Error(w, fmt.Sprintf("clone %q: could not record its spec, so the clone was rolled back: %v", id, err), http.StatusInternalServerError)
		return
	}

	writeJSONStatus(w, http.StatusCreated, struct {
		Name     string   `json:"name"`
		Warnings []string `json:"warnings,omitempty"`
	}{Name: req.Name, Warnings: warnings})
}

// cloneEntry derives the clone's spec from its source: the same machine with
// its own identity.
//
// Passthrough devices are dropped, since a host device can be attached to only
// one running domain; the returned warning says so. UUID and MAC are blanked
// so libvirt generates new ones: a copied UUID fails the define outright.
func cloneEntry(source metadata.VMEntry, name string, start bool) (metadata.VMEntry, []string) {
	e := source
	e.ID = name
	e.Hostname = name
	e.UUID, e.MAC = "", ""
	e.Disks = nil // refilled with the clone's own copies
	// A copy of a service's VM is not part of that service.
	e.Service = ""
	e.SetRunning(start)
	e.StartedAtUnix = 0
	if start {
		e.StartedAtUnix = time.Now().Unix()
	}

	var warnings []string
	if len(e.PCIDevices) > 0 || len(e.USBDevices) > 0 {
		e.PCIDevices, e.USBDevices = nil, nil
		warnings = append(warnings, fmt.Sprintf(
			"%s's passed-through host devices were not copied to %s — a host device can only be attached to one running VM at a time. Add them from the clone's Hardware tab if you meant to move them.",
			source.ID, name))
	}
	return e, warnings
}

// buildCloneSeed writes the clone's seed ISO, reporting a missing xorriso as
// handleVMCreate does.
func (s *Server) buildCloneSeed(source metadata.VMEntry, name, isoPath string) error {
	seed := cloudinit.CloneParams{
		Hostname:   name,
		Username:   source.CloudInitUser,
		Shell:      loginShellFor(source.OS),
		GuestAgent: s.guestAgentFor(source.OS),
		Groups:     adminGroupsFor(source.OS),
	}
	if source.CloudInit != nil {
		seed.SSHKeys = source.CloudInit.SSHKeys
	}

	if err := cloudinit.BuildClone(seed, isoPath); err != nil {
		if pErr := cloudinit.Preflight(); pErr != nil {
			return errors.New("xorriso is not installed on this host. Run: sudo apt install xorriso (Debian/Ubuntu) or sudo dnf install xorriso (Fedora/RHEL)")
		}
		return fmt.Errorf("cloud-init build: %w", err)
	}
	return nil
}
