package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/template"
)

// templateJSON is one saved configuration as the API reports it.
type templateJSON struct {
	Name          string           `json:"name"`
	CreatedAtUnix int64            `json:"created_at_unix"`
	SourceVM      string           `json:"source_vm"`
	Spec          templateSpecJSON `json:"spec"`
}

// templateSpecJSON is shaped like the POST /vms body rather than the stored
// VMEntry, since a template exists to become a create request.
type templateSpecJSON struct {
	ImageID     string       `json:"image_id"`
	OS          string       `json:"os"`
	VCPUs       int          `json:"vcpus"`
	MemoryMB    int          `json:"memory_mb"`
	DiskGB      int          `json:"disk_gb"`
	NetworkMode string       `json:"network_mode"`
	Firmware    string       `json:"firmware"`
	SecureBoot  bool         `json:"secure_boot"`
	TPM         bool         `json:"tpm"`
	CPU         cpuShapeBody `json:"cpu"`
	// Devices are named as in a create request and re-resolved against the host
	// when a VM is created from the template.
	PCIDevices []string              `json:"pci_devices"`
	USBDevices []string              `json:"usb_devices"`
	CloudInit  templateCloudInitJSON `json:"cloud_init"`
}

// templateCloudInitJSON carries the provisioning a template can reproduce. The
// password is never stored, so a VM created from a template asks for one.
type templateCloudInitJSON struct {
	Username   string               `json:"username"`
	Packages   []string             `json:"packages"`
	RunCmd     []string             `json:"runcmd"`
	WriteFiles []metadata.WriteFile `json:"write_files"`
	SSHKeys    []string             `json:"ssh_keys"`
}

// templateCreateRequest is the body of POST /templates, which captures an
// existing VM's configuration.
type templateCreateRequest struct {
	Name   string `json:"name"`
	FromVM string `json:"from_vm"`
}

func templateResponse(t template.Template) templateJSON {
	sel := selectionOf(t.Spec)
	ci := templateCloudInitJSON{Username: t.Spec.CloudInitUser}
	if c := t.Spec.CloudInit; c != nil {
		ci.Packages, ci.RunCmd, ci.WriteFiles, ci.SSHKeys = c.Packages, c.RunCmd, c.WriteFiles, c.SSHKeys
	}

	return templateJSON{
		Name:          t.Name,
		CreatedAtUnix: t.CreatedAtUnix,
		SourceVM:      t.SourceVM,
		Spec: templateSpecJSON{
			ImageID:     t.Spec.ImageID,
			OS:          t.Spec.OS,
			VCPUs:       t.Spec.VCPUs,
			MemoryMB:    t.Spec.MemoryMB,
			DiskGB:      t.Spec.DiskGB,
			NetworkMode: t.Spec.NetworkMode,
			Firmware:    t.Spec.Firmware,
			SecureBoot:  t.Spec.SecureBoot,
			TPM:         t.Spec.TPM,
			CPU:         cpuShapeOf(t.Spec),
			PCIDevices:  sel.PCI,
			USBDevices:  sel.USB,
			CloudInit: templateCloudInitJSON{
				Username:   ci.Username,
				Packages:   nonNil(ci.Packages),
				RunCmd:     nonNil(ci.RunCmd),
				WriteFiles: nonNil(ci.WriteFiles),
				SSHKeys:    nonNil(ci.SSHKeys),
			},
		},
	}
}

// handleTemplateList handles GET /templates.
func (s *Server) handleTemplateList(w http.ResponseWriter, _ *http.Request) {
	list, err := s.templates.List()
	if err != nil {
		http.Error(w, fmt.Sprintf("read templates: %v", err), http.StatusInternalServerError)
		return
	}
	out := make([]templateJSON, 0, len(list))
	for _, t := range list {
		out = append(out, templateResponse(t))
	}
	writeJSON(w, out)
}

// handleTemplateCreate handles POST /templates, capturing a VM's configuration.
func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var req templateCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := template.ValidateName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.FromVM == "" {
		http.Error(w, `"from_vm" is required — a template is captured from a VM`, http.StatusBadRequest)
		return
	}

	entry, ok := s.specEntry(w, req.FromVM)
	if !ok {
		return
	}

	t := template.FromEntry(req.Name, entry)
	t.SourceVM = req.FromVM
	if err := s.templates.Create(t); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, template.ErrExists) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}

	writeJSONStatus(w, http.StatusCreated, templateResponse(t))
}

// handleTemplateDelete handles DELETE /templates/{name}.
func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.templates.Delete(r.PathValue("name")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, template.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
