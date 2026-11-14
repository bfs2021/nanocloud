package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// The disk endpoints live outside PATCH /vms/{id}/spec because each one
// creates, deletes or grows a file on the host.

// diskJSON is one disk as the API reports it.
type diskJSON struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	SizeGB int    `json:"size_gb"`
	// Boot marks the overlay the VM starts from.
	Boot bool `json:"boot"`
}

// diskSizeRequest is the body of both attach and resize: the size in GiB.
type diskSizeRequest struct {
	SizeGB int `json:"size_gb"`
}

// maxDiskPathAttempts bounds the search for a free disk filename.
const maxDiskPathAttempts = 64

// disksOf lists a VM's disks, boot overlay first. The spec's Disks leaves out
// the boot disk, whose path derives from the pool and VM name.
func disksOf(e metadata.VMEntry) []diskJSON {
	out := make([]diskJSON, 0, len(e.Disks)+1)
	out = append(out, diskJSON{
		Target: vm.BootDiskTarget,
		Path:   storage.DiskPathFor(e.PoolPath, e.ID),
		SizeGB: e.DiskGB,
		Boot:   true,
	})
	for _, d := range e.Disks {
		out = append(out, diskJSON{Target: d.Target, Path: d.Path, SizeGB: d.SizeGB})
	}
	return out
}

// handleDiskAttach handles POST /vms/{id}/disks. There is no hotplug: the disk
// reaches the guest at its next boot, which restart_required reports.
func (s *Server) handleDiskAttach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, ok := s.specEntry(w, id)
	if !ok {
		return
	}
	sizeGB, ok := decodeDiskSize(w, r)
	if !ok {
		return
	}
	if entry.PoolPath == "" {
		http.Error(w, fmt.Sprintf("%q has no storage pool recorded, so there is nowhere to put a new disk", id), http.StatusUnprocessableEntity)
		return
	}

	target, err := vm.NextDiskTarget(entry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	path, err := freeDiskPath(entry.PoolPath, id, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := storage.CreateDisk(path, sizeGB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	disk := metadata.Disk{Target: target, Path: path, SizeGB: sizeGB}
	entry.Disks = append(entry.Disks, disk)
	if err := s.writeSpec(id, func(e *metadata.VMEntry) { e.Disks = append(e.Disks, disk) }); err != nil {
		// Nothing references the file yet, so removing it is the complete undo.
		storage.DeleteDisk(path) //nolint:errcheck // best-effort rollback; the 500 is already going out
		http.Error(w, fmt.Sprintf("attach disk to %q: could not record it, so the disk was removed again: %v", id, err), http.StatusInternalServerError)
		return
	}

	s.respondWithSpec(w, id, entry, s.redefine(entry))
}

// handleDiskDetach handles DELETE /vms/{id}/disks/{target}. The file is kept
// unless ?delete_file=true, so detaching never destroys data.
func (s *Server) handleDiskDetach(w http.ResponseWriter, r *http.Request) {
	id, target := r.PathValue("id"), r.PathValue("target")

	entry, ok := s.specEntry(w, id)
	if !ok {
		return
	}
	if target == vm.BootDiskTarget {
		http.Error(w, fmt.Sprintf("%s is the disk %q boots from and cannot be detached — delete the VM instead", target, id), http.StatusBadRequest)
		return
	}

	idx := -1
	for i, d := range entry.Disks {
		if d.Target == target {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, fmt.Sprintf("%q has no disk %s", id, target), http.StatusNotFound)
		return
	}

	path := entry.Disks[idx].Path
	entry.Disks = append(entry.Disks[:idx:idx], entry.Disks[idx+1:]...)
	remaining := entry.Disks
	if err := s.writeSpec(id, func(e *metadata.VMEntry) { e.Disks = remaining }); err != nil {
		http.Error(w, fmt.Sprintf("detach %s from %q: could not record the change: %v", target, id, err), http.StatusInternalServerError)
		return
	}

	redefineErr := s.redefine(entry)
	// Only once the domain no longer references the file; otherwise a failed
	// redefine would leave a VM that cannot start.
	if redefineErr == nil && r.URL.Query().Get("delete_file") == "true" {
		if err := storage.DeleteDisk(path); err != nil {
			http.Error(w, fmt.Sprintf("detached %s from %q, but its file could not be removed: %v", target, id, err), http.StatusInternalServerError)
			return
		}
	}

	s.respondWithSpec(w, id, entry, redefineErr)
}

// handleDiskResize handles POST /vms/{id}/disks/{target}/resize. It needs no
// redefine, since the domain XML records where a disk is but not its size.
func (s *Server) handleDiskResize(w http.ResponseWriter, r *http.Request) {
	id, target := r.PathValue("id"), r.PathValue("target")

	entry, ok := s.specEntry(w, id)
	if !ok {
		return
	}
	sizeGB, ok := decodeDiskSize(w, r)
	if !ok {
		return
	}

	path, boot := diskPathOf(entry, target)
	if path == "" {
		http.Error(w, fmt.Sprintf("%q has no disk %s", id, target), http.StatusNotFound)
		return
	}

	// qemu-img cannot take the write lock while QEMU holds it, and its lock
	// error does not say "stop the VM". Only a known shut-off state counts: a
	// paused guest still has its disks open.
	v, err := s.vmManager.Get(id)
	if err != nil {
		writeVMManagerError(w, fmt.Sprintf("check whether %q is stopped before resizing %s", id, target), err)
		return
	}
	if v.State != "stopped" {
		http.Error(w, fmt.Sprintf("%q is %s — stop it before resizing %s, because the disk cannot be written to while QEMU has it open", id, v.State, target), http.StatusConflict)
		return
	}

	info, err := storage.InspectDisk(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.Snapshots > 0 {
		http.Error(w, fmt.Sprintf("%s holds %d snapshot(s) inside the image, and a disk with snapshots cannot be resized — delete them first", target, info.Snapshots), http.StatusConflict)
		return
	}
	if want := int64(sizeGB) * storage.BytesPerGB; want <= info.VirtualSizeBytes {
		http.Error(w, fmt.Sprintf("%s is already %d GB — a disk can only be grown, because shrinking one discards whatever the guest had written past the new end",
			target, info.VirtualSizeBytes/storage.BytesPerGB), http.StatusBadRequest)
		return
	}

	if err := storage.ResizeDisk(path, sizeGB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Recorded only after the resize succeeds.
	if err := s.writeSpec(id, func(e *metadata.VMEntry) { setDiskSize(e, target, boot, sizeGB) }); err != nil {
		http.Error(w, fmt.Sprintf("resized %s on %q, but could not record its new size: %v", target, id, err), http.StatusInternalServerError)
		return
	}
	setDiskSize(&entry, target, boot, sizeGB)

	s.respondWithSpec(w, id, entry, nil)
}

// decodeDiskSize reads the requested size, writing the error response itself
// when it is not usable.
func decodeDiskSize(w http.ResponseWriter, r *http.Request) (int, bool) {
	var req diskSizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return 0, false
	}
	if req.SizeGB <= 0 {
		http.Error(w, `"size_gb" must be > 0`, http.StatusBadRequest)
		return 0, false
	}
	return req.SizeGB, true
}

// diskPathOf resolves a target to the file behind it. Unlike detach, resize
// accepts the boot disk.
func diskPathOf(e metadata.VMEntry, target string) (path string, boot bool) {
	if target == vm.BootDiskTarget {
		return storage.DiskPathFor(e.PoolPath, e.ID), true
	}
	for _, d := range e.Disks {
		if d.Target == target {
			return d.Path, false
		}
	}
	return "", false
}

// setDiskSize records a disk's new size: DiskGB for the boot overlay, the
// disk's own SizeGB otherwise.
func setDiskSize(e *metadata.VMEntry, target string, boot bool, sizeGB int) {
	if boot {
		e.DiskGB = sizeGB
		return
	}
	for i := range e.Disks {
		if e.Disks[i].Target == target {
			e.Disks[i].SizeGB = sizeGB
			return
		}
	}
}

// freeDiskPath picks the filename for a new disk, stepping past names already
// on disk: a detached disk keeps its file, so a freed target letter can come
// back while that file still exists.
func freeDiskPath(poolPath, vmName, target string) (string, error) {
	base := filepath.Join(poolPath, vmName+"-"+target)
	for i := 1; i <= maxDiskPathAttempts; i++ {
		path := base + storage.DiskExt
		if i > 1 {
			path = fmt.Sprintf("%s-%d%s", base, i, storage.DiskExt)
		}
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
	}
	return "", fmt.Errorf("no free filename for a new %s in %s — %d are already there; remove the ones no VM uses", target, poolPath, maxDiskPathAttempts)
}

// respondWithSpec ends every disk request with the whole spec. On a redefine
// failure the spec is kept: the change stays recorded but unapplied, and
// restart_required says so.
func (s *Server) respondWithSpec(w http.ResponseWriter, id string, entry metadata.VMEntry, redefineErr error) {
	if redefineErr != nil {
		writeVMManagerError(w, fmt.Sprintf("apply disk change to %q", id), redefineErr)
		return
	}
	writeJSON(w, s.specResponseFor(entry))
}
