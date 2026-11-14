package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/nanocloud-io/nanocloud/internal/atomicfile"
)

// CurrentSchemaVersion is the services.json format this binary writes.
// Adding fields to Instance does not need a bump; a restructure would.
const CurrentSchemaVersion = 1

// ErrSchemaTooNew is returned when services.json was written by a newer
// binary. Writing it back would strip every field this binary does not know.
var ErrSchemaTooNew = errors.New("services: file schema is newer than this binary understands")

// The build states in order, then the two terminal ones. A worker drives a
// service in a building state; ready and failed have none.
const (
	StatePulling    = "pulling"
	StateCreating   = "creating"
	StateWaitingIP  = "waiting-ip"
	StateInstalling = "installing"
	StateReady      = "ready"
	StateFailed     = "failed"
)

// building reports whether a state is one a worker drives forward.
func building(state string) bool {
	switch state {
	case StatePulling, StateCreating, StateWaitingIP, StateInstalling:
		return true
	}
	return false
}

// Instance is one service as recorded in services.json. It holds the
// service's secrets, which is why the file is written 0600.
type Instance struct {
	Name   string `json:"name"`
	Recipe string `json:"recipe"`
	Size   string `json:"size"`
	State  string `json:"state"`
	// Reason says why a failed service failed, as a sentence for its user.
	Reason string `json:"reason,omitempty"`
	// FailedIn is the step a failed service was on when it failed.
	FailedIn string `json:"failed_in,omitempty"`
	// ImageID is the catalog image chosen for this host, set once resolved.
	ImageID string `json:"image_id,omitempty"`
	// VMs are the service's machines; the first one's address is the
	// service's. Members gives each VM's role; without it, the one VM plays
	// the recipe's only role.
	VMs     []string `json:"vms"`
	Members []Member `json:"members,omitempty"`
	// Params holds every template value, generated secrets included, fixed
	// at create time.
	Params map[string]string `json:"params"`
	// VMPassword is the console password of the VM's nanocloud user.
	VMPassword string `json:"vm_password"`
	// SSHKeys are the user's keys, authorised alongside the daemon's.
	SSHKeys []string `json:"ssh_keys,omitempty"`
	// GitHubUser imports that account's keys, the same way a VM create does.
	GitHubUser string `json:"github_user,omitempty"`
	// HostKeys are the VMs' SSH host keys, recorded on first contact and
	// required to match afterwards. HostKey is the legacy single-VM field.
	HostKeys map[string]string `json:"host_keys,omitempty"`
	HostKey  string            `json:"host_key,omitempty"`
	// Ports are the gateway's host ports by endpoint ID, fixed so addresses
	// never change. Only this host can connect to them unless Shared.
	Ports         map[string]int `json:"ports,omitempty"`
	Shared        bool           `json:"shared,omitempty"`
	CreatedAtUnix int64          `json:"created_at_unix"`
	ReadyAtUnix   int64          `json:"ready_at_unix,omitempty"`
	// Adding and Removing are workers on their way in or out. Published are
	// NodePorts published through the gateway, each with its port in Ports.
	Adding    []string    `json:"adding,omitempty"`
	Removing  []string    `json:"removing,omitempty"`
	Published []Published `json:"published,omitempty"`
	// RetriedAtUnix is when the service was last retried after a failure;
	// VMs then get the full no-network wait again.
	RetriedAtUnix int64 `json:"retried_at_unix,omitempty"`
	// Watched lists the exporters set up on the service's VMs as
	// "<vm>/<job>". On a monitoring service, Provisioned is the hash of the
	// monitoring bundle last installed there.
	Watched     []string `json:"watched,omitempty"`
	Provisioned string   `json:"provisioned,omitempty"`
}

// Member is one VM of a service and the role it plays.
type Member struct {
	Role string `json:"role"`
	VM   string `json:"vm"`
}

// members returns the service's VMs with their roles.
func (inst Instance) members() []Member {
	if len(inst.Members) > 0 {
		return inst.Members
	}
	out := make([]Member, 0, len(inst.VMs))
	for _, vm := range inst.VMs {
		out = append(out, Member{Role: mainRole, VM: vm})
	}
	if len(out) == 0 {
		out = append(out, Member{Role: mainRole, VM: inst.Name})
	}
	return out
}

// hostKey returns the SSH host key recorded for one of the service's VMs.
func (inst Instance) hostKey(vm string) string {
	if k := inst.HostKeys[vm]; k != "" {
		return k
	}
	if vm == primaryVM(inst) {
		return inst.HostKey
	}
	return ""
}

// Store is the file-backed collection of services, keyed by name.
type Store struct {
	file *atomicfile.File
}

// NewStore returns a Store backed by the file at path, which need not exist.
func NewStore(path string) *Store {
	return &Store{file: atomicfile.NewPrivate(path)}
}

type document struct {
	SchemaVersion int                 `json:"schema_version"`
	Services      map[string]Instance `json:"services"`
}

func decode(data []byte) (map[string]Instance, error) {
	if len(data) == 0 {
		return make(map[string]Instance), nil
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("services: parse document: %w", err)
	}
	if doc.SchemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: file is v%d, this binary understands v%d",
			ErrSchemaTooNew, doc.SchemaVersion, CurrentSchemaVersion)
	}
	if doc.Services == nil {
		doc.Services = make(map[string]Instance)
	}
	return doc.Services, nil
}

func encode(services map[string]Instance) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(document{SchemaVersion: CurrentSchemaVersion, Services: services}); err != nil {
		return nil, fmt.Errorf("services: encode document: %w", err)
	}
	return buf.Bytes(), nil
}

// List returns every service, ordered by name.
func (s *Store) List() ([]Instance, error) {
	data, err := s.file.Read()
	if err != nil {
		return nil, err
	}
	all, err := decode(data)
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(all))
	for _, inst := range all {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one service, or ErrNotFound.
func (s *Store) Get(name string) (Instance, error) {
	data, err := s.file.Read()
	if err != nil {
		return Instance{}, err
	}
	all, err := decode(data)
	if err != nil {
		return Instance{}, err
	}
	inst, ok := all[name]
	if !ok {
		return Instance{}, errorOf(ErrNotFound, "there is no service named %q", name)
	}
	return inst, nil
}

// Create stores a new service, refusing a name already taken.
func (s *Store) Create(inst Instance) error {
	return s.file.Update(func(data []byte) ([]byte, error) {
		all, err := decode(data)
		if err != nil {
			return nil, err
		}
		if _, taken := all[inst.Name]; taken {
			return nil, errorOf(ErrExists, "a service named %q already exists", inst.Name)
		}
		all[inst.Name] = inst
		return encode(all)
	})
}

// Update applies fn to a stored service, or returns ErrNotFound.
func (s *Store) Update(name string, fn func(*Instance)) error {
	return s.file.Update(func(data []byte) ([]byte, error) {
		all, err := decode(data)
		if err != nil {
			return nil, err
		}
		inst, ok := all[name]
		if !ok {
			return nil, errorOf(ErrNotFound, "there is no service named %q", name)
		}
		fn(&inst)
		all[name] = inst
		return encode(all)
	})
}

// Delete removes a service, or returns ErrNotFound.
func (s *Store) Delete(name string) error {
	return s.file.Update(func(data []byte) ([]byte, error) {
		all, err := decode(data)
		if err != nil {
			return nil, err
		}
		if _, ok := all[name]; !ok {
			return nil, errorOf(ErrNotFound, "there is no service named %q", name)
		}
		delete(all, name)
		return encode(all)
	})
}
