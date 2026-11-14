package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// Errors the API maps to status codes. Returned errors wrap one of these but
// carry their own message.
var (
	ErrNotFound    = errors.New("services: no service with that name")
	ErrExists      = errors.New("services: name already taken")
	ErrInvalid     = errors.New("services: invalid request")
	ErrNotReady    = errors.New("services: the service is not ready")
	ErrUnavailable = errors.New("services: the services manager is not running")
)

// kindError is an error of one of the kinds above with its own message.
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

func errorOf(kind error, format string, args ...any) error {
	return &kindError{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// Display states derived from the VMs' current state; they are never stored.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateMissing  = "missing"
)

// namePattern keeps a service name usable as a VM name and a hostname.
var namePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$`)

// installPath and the two files beside it are the contract between the
// install script (see installScript) and the manager watching it.
const (
	installPath = "/usr/local/sbin/nanocloud-service-install"
	stateFile   = "/var/lib/nanocloud/service-state"
	logFile     = "/var/log/nanocloud-service.log"
)

// Manager builds and runs services. Create starts a worker that drives a
// service from pulling to ready; Run resumes half-built ones.
type Manager struct {
	store  *Store
	events *events.Ring
	guest  Guest
	// pubKey is the daemon's key as an authorized_keys line.
	pubKey string
	// dial checks that a port answers; a field so tests need no VM.
	dial func(ctx context.Context, addr string) error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	api      *vmAPI
	workers  map[string]*worker
	progress map[string]int

	// poll is the worker's retry interval; installTimeout bounds how long an
	// install may run while its VMs are up.
	poll           time.Duration
	installTimeout time.Duration
	// noNetworkAfter is longer than the VM's own no-network grace period
	// because a service's first boot also runs its install.
	noNetworkAfter time.Duration

	// gw forwards host ports to the services' endpoints. Ports are allocated
	// from portBase..portBase+portSpan where portFree reports them unused;
	// portMu keeps two creates from taking the same one.
	gw           *Gateway
	portBase     int
	portSpan     int
	portFree     func(port int) bool
	portMu       sync.Mutex
	syncMu       sync.Mutex
	gatewayRetry time.Duration

	// vmCache is a recent VM listing for the gateway (recentVMs).
	vmMu       sync.Mutex
	vmCache    map[string]vmView
	vmCacheAt  time.Time
	vmCacheTTL time.Duration

	kubeMu   sync.Mutex
	clusters map[string]clusterReading

	// reach is the last look at each building service's VMs.
	reachMu sync.Mutex
	reach   map[string]map[string]memberLook

	// promURL is where a monitoring VM's Prometheus answers.
	mon          monitorState
	monitorEvery time.Duration
	setupRetry   time.Duration
	promURL      func(ip string) string
	httpc        *http.Client
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	// again asks for another build once this one returns: a worker that has
	// just written its last state is still registered for a moment.
	again bool
}

// New returns a Manager keeping its services in store and its SSH identity
// at keyPath, created there on first use.
func New(store *Store, ring *events.Ring, keyPath string) (*Manager, error) {
	signer, err := LoadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	m := newManager(store, ring, &sshGuest{signer: signer, port: "22"}, authorizedKey(signer.PublicKey()))
	return m, nil
}

func newManager(store *Store, ring *events.Ring, guest Guest, pubKey string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:          store,
		events:         ring,
		guest:          guest,
		pubKey:         pubKey,
		dial:           dialTCP,
		ctx:            ctx,
		cancel:         cancel,
		workers:        map[string]*worker{},
		progress:       map[string]int{},
		poll:           3 * time.Second,
		installTimeout: 30 * time.Minute,
		noNetworkAfter: 10 * time.Minute,
		portBase:       gatewayPortBase,
		portSpan:       gatewayPortSpan,
		portFree:       portFree,
		gatewayRetry:   15 * time.Second,
		vmCacheTTL:     2 * time.Second,
		clusters:       map[string]clusterReading{},
		reach:          map[string]map[string]memberLook{},
		mon:            newMonitorState(),
		monitorEvery:   30 * time.Second,
		setupRetry:     15 * time.Minute,
		promURL:        func(ip string) string { return "http://" + net.JoinHostPort(ip, "9090") },
		httpc:          &http.Client{Timeout: 5 * time.Second},
	}
	m.gw = newGateway(m.resolveEndpoint)
	return m
}

func dialTCP(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// Attach gives the manager the daemon's own API, which it uses to pull images
// and manage VMs. Nothing works until it is called.
func (m *Manager) Attach(h http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.api = &vmAPI{h: h}
}

func (m *Manager) client() (*vmAPI, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.api == nil {
		return nil, ErrUnavailable
	}
	return m.api, nil
}

// Run resumes every half-built service and opens the gateway, then blocks
// until ctx ends. An interrupted build resumes from the same step next time.
func (m *Manager) Run(ctx context.Context) {
	list, err := m.store.List()
	if err != nil {
		log.Printf("services: cannot read services, none resumed: %v", err)
	}
	for _, inst := range list {
		if building(inst.State) {
			m.startWorker(inst.Name)
		}
		for _, vm := range inst.Removing {
			m.startRemoval(inst.Name, vm)
		}
	}
	m.backfillPorts()
	m.syncGateway()
	m.wg.Add(2)
	go m.keepGateway()
	go m.keepMonitoring()
	<-ctx.Done()
	m.cancel()
	m.gw.close()
	m.wg.Wait()
}

// CreateRequest is a request to create a service.
type CreateRequest struct {
	Name   string            `json:"name"`
	Recipe string            `json:"recipe"`
	Size   string            `json:"size"`
	Params map[string]string `json:"params"`
	// SSHPublicKey is one or more authorized_keys lines to trust on the VM.
	SSHPublicKey string `json:"ssh_public_key"`
	// GitHubUser imports that account's published keys.
	GitHubUser string `json:"github_user"`
}

// Create records a new service and starts building it.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (View, error) {
	api, err := m.client()
	if err != nil {
		return View{}, err
	}
	inst, err := newInstance(req)
	if err != nil {
		return View{}, err
	}
	vms, err := api.vms(ctx)
	if err != nil {
		return View{}, listError(err)
	}
	for _, vm := range inst.VMs {
		if _, taken := vms[vm]; taken {
			return View{}, errorOf(ErrExists, "a VM named %q already exists", vm)
		}
	}
	if err := m.createWithPorts(&inst); err != nil {
		return View{}, err
	}
	m.syncGateway()
	m.startWorker(inst.Name)
	return m.view(ctx, inst, vms, true), nil
}

func (m *Manager) createWithPorts(inst *Instance) error {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	list, err := m.store.List()
	if err != nil {
		return err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	inst.Ports = m.allocatePorts(recipe.Endpoints, nil, takenPorts(list))
	return m.store.Create(*inst)
}

// newInstance validates a request and fixes every value the service will
// use, generating its secrets.
func newInstance(req CreateRequest) (Instance, error) {
	name := strings.TrimSpace(req.Name)
	if !namePattern.MatchString(name) {
		return Instance{}, errorOf(ErrInvalid, "a name is lowercase letters, digits and dashes, starts with a letter and is at most 40 characters")
	}
	if name == "catalog" {
		// Collides with GET /services/catalog.
		return Instance{}, errorOf(ErrInvalid, "%q is reserved; choose another name", name)
	}
	recipe, ok := RecipeByID(req.Recipe)
	if !ok {
		return Instance{}, errorOf(ErrInvalid, "unknown service %q", req.Recipe)
	}
	size, ok := recipe.Size(req.Size)
	if !ok {
		return Instance{}, errorOf(ErrInvalid, "%s has no size %q", recipe.Name, req.Size)
	}

	params := map[string]string{}
	known := map[string]Param{}
	for _, p := range recipe.Params {
		known[p.ID] = p
	}
	for id := range req.Params {
		p, ok := known[id]
		switch {
		case !ok:
			return Instance{}, errorOf(ErrInvalid, "%s has no setting %q", recipe.Name, id)
		case p.Generate > 0:
			return Instance{}, errorOf(ErrInvalid, "%s is generated and cannot be set", p.Label)
		}
	}
	for _, p := range recipe.Params {
		if p.Generate > 0 {
			v, err := randomString(p.Generate, p.Charset)
			if err != nil {
				return Instance{}, err
			}
			params[p.ID] = v
			continue
		}
		v := strings.TrimSpace(req.Params[p.ID])
		if v == "" {
			v = p.Default
		}
		if p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(v) {
			msg := fmt.Sprintf("%s %q is not allowed", p.Label, v)
			if p.Hint != "" {
				msg += ": use " + p.Hint
			}
			return Instance{}, errorOf(ErrInvalid, "%s", msg)
		}
		params[p.ID] = v
	}

	var keys []string
	for _, line := range strings.Split(req.SSHPublicKey, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return Instance{}, errorOf(ErrInvalid, "not an SSH public key: %q", truncate(line, 40))
		}
		key := authorizedKey(k)
		if comment != "" {
			key += " " + comment
		}
		keys = append(keys, key)
	}

	members, err := planMembers(name, recipe, params)
	if err != nil {
		return Instance{}, err
	}
	vms := make([]string, 0, len(members))
	for _, mem := range members {
		vms = append(vms, mem.VM)
	}

	password, err := randomString(20, charsetAlnum)
	if err != nil {
		return Instance{}, err
	}
	return Instance{
		Name:          name,
		Recipe:        recipe.ID,
		Size:          size.ID,
		State:         StatePulling,
		VMs:           vms,
		Members:       members,
		Params:        params,
		VMPassword:    password,
		SSHKeys:       keys,
		GitHubUser:    strings.TrimSpace(req.GitHubUser),
		CreatedAtUnix: time.Now().Unix(),
	}, nil
}

// planMembers names a service's VMs. A single-VM recipe's VM takes the
// service's name; otherwise each is named after the service and its role,
// numbered when the role has several: db-server, db-agent-1.
func planMembers(name string, recipe Recipe, params map[string]string) ([]Member, error) {
	if len(recipe.Roles) == 0 {
		return []Member{{Role: mainRole, VM: name}}, nil
	}
	var out []Member
	for _, role := range recipe.Roles {
		n := role.Count
		if role.CountParam != "" {
			var err error
			n, err = strconv.Atoi(params[role.CountParam])
			if err != nil || n < 1 {
				return nil, errorOf(ErrInvalid, "%s needs a number of machines of 1 or more", role.Label)
			}
		}
		base := name + "-" + strings.ReplaceAll(role.ID, "_", "-")
		for i := 1; i <= n; i++ {
			vm := base
			if n > 1 {
				vm = fmt.Sprintf("%s-%d", base, i)
			}
			if len(vm) > 63 {
				return nil, errorOf(ErrInvalid, "the name is too long for its machines' names; use a shorter one")
			}
			out = append(out, Member{Role: role.ID, VM: vm})
		}
	}
	return out, nil
}

// View is a service as the API shows it: what is stored, with its VMs'
// current state laid over it.
type View struct {
	Name          string        `json:"name"`
	Recipe        string        `json:"recipe"`
	RecipeName    string        `json:"recipe_name"`
	Category      string        `json:"category"`
	Size          Size          `json:"size"`
	State         string        `json:"state"`
	Reason        string        `json:"reason,omitempty"`
	FailedIn      string        `json:"failed_in,omitempty"`
	Progress      int           `json:"progress,omitempty"`
	VMs           []string      `json:"vms"`
	IP            string        `json:"ip,omitempty"`
	CreatedAtUnix int64         `json:"created_at_unix"`
	ReadyAtUnix   int64         `json:"ready_at_unix,omitempty"`
	Outputs       []OutputValue `json:"outputs"`
	Files         []File        `json:"files"`
	// Endpoints are reached through the gateway; Shared says whether other
	// machines may connect, or only this host.
	Endpoints []EndpointView `json:"endpoints"`
	Shared    bool           `json:"shared"`
	// Console selects the console's extra tabs ("kubernetes"). Scale is the
	// role machines can be added to or removed from.
	Console  string     `json:"console,omitempty"`
	Scale    *ScaleView `json:"scale,omitempty"`
	Adding   []string   `json:"adding,omitempty"`
	Removing []string   `json:"removing,omitempty"`
	// Alerts are those a monitoring service has firing about this one.
	Alerts []Alert `json:"alerts,omitempty"`
	// Members are the service's VMs with their roles and current state.
	Members []MemberView `json:"members"`
	// VMUser and VMPassword log in at the VMs' consoles.
	VMUser     string `json:"vm_user"`
	VMPassword string `json:"vm_password"`
}

// MemberView is one of a service's VMs as it is now.
type MemberView struct {
	Role      string `json:"role"`
	RoleLabel string `json:"role_label"`
	VM        string `json:"vm"`
	// Status and Phase come from GET /vms. Status is "missing" when the VM no
	// longer exists and empty when the hypervisor could not be asked.
	Status string `json:"status"`
	Phase  string `json:"phase,omitempty"`
	IP     string `json:"ip,omitempty"`
	// Set during a build and after a failed one: the VM's last install state,
	// the role it waits for, or why it cannot be reached and since when.
	Install          string `json:"install,omitempty"`
	Failure          string `json:"failure,omitempty"`
	WaitsFor         string `json:"waits_for,omitempty"`
	Unreachable      string `json:"unreachable,omitempty"`
	UnreachableSince int64  `json:"unreachable_since,omitempty"`
}

// OutputValue is a recipe output with its value filled in.
type OutputValue struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// List returns every service.
func (m *Manager) List(ctx context.Context) ([]View, error) {
	list, err := m.store.List()
	if err != nil {
		return nil, err
	}
	vms, known := m.liveVMs(ctx)
	out := make([]View, 0, len(list))
	for _, inst := range list {
		out = append(out, m.view(ctx, inst, vms, known))
	}
	return out, nil
}

// Get returns one service.
func (m *Manager) Get(ctx context.Context, name string) (View, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return View{}, err
	}
	vms, known := m.liveVMs(ctx)
	return m.view(ctx, inst, vms, known), nil
}

// liveVMs lists the VMs and reports whether it could, so an unreachable
// hypervisor shows a service's stored state rather than "missing".
func (m *Manager) liveVMs(ctx context.Context) (map[string]vmView, bool) {
	api, err := m.client()
	if err != nil {
		return nil, false
	}
	vms, err := api.vms(ctx)
	if err != nil {
		return nil, false
	}
	return vms, true
}

// view is how a service looks to the person in ctx (see ViewedFrom).
func (m *Manager) view(ctx context.Context, inst Instance, vms map[string]vmView, known bool) View {
	recipe, _ := RecipeByID(inst.Recipe)
	size, _ := recipe.Size(inst.Size)
	v := View{
		Name:          inst.Name,
		Recipe:        inst.Recipe,
		RecipeName:    recipe.Name,
		Category:      recipe.Category,
		Size:          size,
		State:         inst.State,
		Reason:        inst.Reason,
		FailedIn:      inst.FailedIn,
		VMs:           inst.VMs,
		CreatedAtUnix: inst.CreatedAtUnix,
		ReadyAtUnix:   inst.ReadyAtUnix,
		Outputs:       []OutputValue{},
		Files:         []File{},
		Shared:        inst.Shared,
		Console:       recipe.Console,
		Scale:         scaleView(inst, recipe),
		Adding:        inst.Adding,
		Removing:      inst.Removing,
		Alerts:        m.alertsOn(inst.Name),
		VMUser:        serviceUser,
		VMPassword:    inst.VMPassword,
	}
	if inst.State == StatePulling {
		m.mu.Lock()
		v.Progress = m.progress[inst.Name]
		m.mu.Unlock()
	}
	running, missing, notReady := 0, "", false
	var looks map[string]memberLook
	if building(inst.State) || inst.State == StateFailed {
		looks = m.looksAt(inst.Name)
	}
	for _, mem := range inst.members() {
		role, _ := recipe.role(mem.Role)
		mv := MemberView{Role: mem.Role, RoleLabel: role.Label, VM: mem.VM}
		if l, ok := looks[mem.VM]; ok {
			mv.Install, mv.Failure, mv.Unreachable = l.state, l.failure, l.unreachable
			if !l.since.IsZero() {
				mv.UnreachableSince = l.since.Unix()
			}
			if l.state == "waiting" && role.After != "" {
				waitsFor, _ := recipe.role(role.After)
				mv.WaitsFor = waitsFor.Label
			}
		}
		if vm, ok := vms[mem.VM]; ok {
			mv.Status, mv.Phase = vm.Status, vm.Phase
			if vm.Phase == "ready" {
				mv.IP = vm.IP
			}
			if vm.Status == "running" {
				running++
			}
			if vm.Status != "running" || vm.Phase != "ready" {
				notReady = true
			}
		} else if known {
			mv.Status = StateMissing
			if missing == "" {
				missing = mem.VM
			}
		}
		v.Members = append(v.Members, mv)
	}
	if known && inst.State == StateReady {
		switch {
		case missing != "":
			v.State = StateMissing
			v.Reason = fmt.Sprintf("Its VM %q no longer exists.", missing)
		case running == 0:
			v.State = StateStopped
		case notReady:
			v.State = StateStarting
		}
	}
	ips := roleIPs(inst, vms)
	v.IP = ips[""]
	v.Endpoints = m.endpointViews(ctx, inst, recipe, ips)
	if v.State == StateReady && v.IP != "" {
		values := templateValues(inst, ips)
		for k, val := range endpointValues(v.Endpoints) {
			values[k] = val
		}
		for _, o := range recipe.Outputs {
			val, err := render(o.Value, values)
			if err != nil {
				continue // validated at load; unreachable short of a corrupted file
			}
			v.Outputs = append(v.Outputs, OutputValue{ID: o.ID, Label: o.Label, Value: val, Secret: o.Secret})
		}
		v.Files = append(v.Files, recipe.Files...)
	}
	return v
}

func primaryVM(inst Instance) string {
	if len(inst.VMs) == 0 {
		return inst.Name
	}
	return inst.VMs[0]
}

// roleIPs maps each role to its first confirmed VM address, and the service
// itself (its first VM) to "".
func roleIPs(inst Instance, vms map[string]vmView) map[string]string {
	ips := map[string]string{}
	for i, mem := range inst.members() {
		vm, ok := vms[mem.VM]
		if !ok || vm.Phase != "ready" || vm.IP == "" {
			continue
		}
		if i == 0 {
			ips[""] = vm.IP
		}
		if _, seen := ips[mem.Role]; !seen {
			ips[mem.Role] = vm.IP
		}
	}
	return ips
}

// templateValues are the values a recipe's templates see: the params, name,
// machine count and known addresses ({{.ip}}, {{.<role>_ip}}).
func templateValues(inst Instance, ips map[string]string) map[string]string {
	values := make(map[string]string, len(inst.Params)+len(ips)+2)
	for k, v := range inst.Params {
		values[k] = v
	}
	values["name"] = inst.Name
	values["nodes"] = strconv.Itoa(len(inst.members()))
	for role, ip := range ips {
		if role == "" {
			values["ip"] = ip
		} else {
			values[role+"_ip"] = ip
		}
	}
	return values
}

// Start starts a service's VMs.
func (m *Manager) Start(ctx context.Context, name string) error {
	return m.eachVM(ctx, name, (*vmAPI).startVM)
}

// Stop asks a service's VMs to shut down.
func (m *Manager) Stop(ctx context.Context, name string) error {
	return m.eachVM(ctx, name, (*vmAPI).stopVM)
}

func (m *Manager) eachVM(ctx context.Context, name string, fn func(*vmAPI, context.Context, string) error) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	inst, err := m.store.Get(name)
	if err != nil {
		return err
	}
	for _, vm := range inst.VMs {
		if err := fn(api, ctx, vm); err != nil {
			return fmt.Errorf("%s: %w", vm, err)
		}
	}
	return nil
}

// Delete stops a service's worker, deletes its VMs and forgets it.
func (m *Manager) Delete(ctx context.Context, name string) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	inst, err := m.store.Get(name)
	if err != nil {
		return err
	}
	// Stop the worker first, or one mid-create could make a VM after the
	// listing below.
	m.stopWorker(name)
	vms, err := api.vms(ctx)
	if err != nil {
		return listError(err)
	}
	for _, vm := range inst.VMs {
		if _, ok := vms[vm]; !ok {
			continue
		}
		if err := api.deleteVM(ctx, vm); err != nil {
			return fmt.Errorf("delete VM %s: %w", vm, err)
		}
	}
	if err := m.store.Delete(name); err != nil {
		return err
	}
	m.forgetCluster(name)
	m.forgetMonitoring(name)
	m.forgetLooks(name)
	m.syncGateway()
	return nil
}

// File reads one of a ready service's files from its VM.
func (m *Manager) File(ctx context.Context, name, id string) (filename string, data []byte, err error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return "", nil, err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	var file *File
	for i := range recipe.Files {
		if recipe.Files[i].ID == id {
			file = &recipe.Files[i]
		}
	}
	if file == nil {
		return "", nil, errorOf(ErrNotFound, "%s has no file %q", recipe.Name, id)
	}
	vms, known := m.liveVMs(ctx)
	v := m.view(ctx, inst, vms, known)
	if v.State != StateReady || v.IP == "" {
		return "", nil, errorOf(ErrNotReady, "start %s and wait for it to be ready", inst.Name)
	}

	out, _, err := m.guest.Run(ctx, v.IP, inst.hostKey(primaryVM(inst)), "sudo -n cat -- "+shellQuote(file.Path))
	if err != nil {
		return "", nil, fmt.Errorf("read %s from %s: %w", file.Path, inst.Name, err)
	}
	values := templateValues(inst, roleIPs(inst, vms))
	for k, val := range endpointValues(v.Endpoints) {
		values[k] = val
	}
	for from, to := range file.Replace {
		f, ferr := render(from, values)
		t, terr := render(to, values)
		if ferr != nil || terr != nil {
			continue
		}
		out = strings.ReplaceAll(out, f, t)
	}
	filename, err = render(file.Filename, values)
	if err != nil {
		return "", nil, err
	}
	return filename, []byte(out), nil
}

func (m *Manager) startWorker(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, running := m.workers[name]; running {
		w.again = true
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	w := &worker{cancel: cancel, done: make(chan struct{})}
	m.workers[name] = w
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(w.done)
		defer func() {
			m.mu.Lock()
			again := w.again && ctx.Err() == nil // a stopped worker is not run again
			if m.workers[name] == w {
				delete(m.workers, name)
			}
			delete(m.progress, name)
			m.mu.Unlock()
			cancel()
			if again {
				m.startWorker(name)
			}
		}()
		m.build(ctx, name)
	}()
}

func (m *Manager) stopWorker(name string) {
	m.mu.Lock()
	w, ok := m.workers[name]
	m.mu.Unlock()
	if !ok {
		return
	}
	w.cancel()
	<-w.done
}

// build drives one service from its stored state to ready or failed. Every
// step is idempotent, because a restart re-runs the step it interrupted.
func (m *Manager) build(ctx context.Context, name string) {
	// A failed build's looks stay for the console; Retry and Delete drop them.
	defer func() {
		if inst, err := m.store.Get(name); err != nil || inst.State != StateFailed {
			m.forgetLooks(name)
		}
	}()
	for {
		inst, err := m.store.Get(name)
		if err != nil {
			return // deleted
		}
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			m.fail(name, fmt.Errorf("this nano-cloud has no %q service", inst.Recipe))
			return
		}

		var next string
		switch inst.State {
		case StatePulling:
			var imageID string
			imageID, err = m.pullImage(ctx, inst, recipe)
			if err == nil {
				err = m.store.Update(name, func(i *Instance) { i.ImageID = imageID })
			}
			next = StateCreating
		case StateCreating:
			err = m.createVM(ctx, inst, recipe)
			next = StateWaitingIP
		case StateWaitingIP:
			err = m.waitForNetwork(ctx, inst)
			next = StateInstalling
		case StateInstalling:
			err = m.waitForInstall(ctx, inst, recipe)
			next = StateReady
		default:
			return
		}
		if ctx.Err() != nil {
			return // shutting down or deleted; the step re-runs on resume
		}
		if err != nil {
			m.fail(name, err)
			return
		}
		if err := m.store.Update(name, func(i *Instance) {
			i.State = next
			if next == StateReady {
				i.ReadyAtUnix = time.Now().Unix()
				i.Adding = nil
			}
		}); err != nil {
			log.Printf("services: %s: record state %s: %v", name, next, err)
			return
		}
		if next == StateReady && len(inst.Adding) > 0 {
			// Machines were added to a service that was already ready.
			m.note(name, fmt.Sprintf("%s joined %s.", strings.Join(inst.Adding, ", "), name))
			m.forgetCluster(name)
			return
		}
		if next == StateReady {
			m.events.Append(events.Event{
				Level:   events.LevelInfo,
				Kind:    events.KindServiceReady,
				VM:      primaryVM(inst),
				Message: fmt.Sprintf("%s %s is ready.", recipe.Name, name),
			})
			return
		}
		if msg := stepNote(inst, inst.State); msg != "" {
			m.note(name, msg)
		}
	}
}

func (m *Manager) fail(name string, cause error) {
	reason := sentence(cause.Error())
	if err := m.store.Update(name, func(i *Instance) {
		if building(i.State) {
			i.FailedIn = i.State
		}
		i.State = StateFailed
		i.Reason = reason
	}); err != nil {
		log.Printf("services: %s: record failure %q: %v", name, reason, err)
	}
	m.events.Append(events.Event{
		Level:   events.LevelError,
		Kind:    events.KindServiceFailed,
		VM:      name,
		Message: fmt.Sprintf("Service %s failed.", name),
		Detail:  reason,
	})
}

func (m *Manager) sleep(ctx context.Context) error {
	t := time.NewTimer(m.poll)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// listError wraps a failed VM listing, as ErrUnavailable when the hypervisor
// is unreachable, since that is transient and not the request's fault.
func listError(err error) error {
	if transient(err) {
		return errorOf(ErrUnavailable, "the hypervisor is not reachable right now: %v", err)
	}
	return fmt.Errorf("list VMs: %w", err)
}

// transient reports whether an API error should be waited out rather than
// fail the service: a 503 means the hypervisor is momentarily unreachable.
func transient(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusServiceUnavailable
}

func (m *Manager) pullImage(ctx context.Context, inst Instance, recipe Recipe) (string, error) {
	api, err := m.client()
	if err != nil {
		return "", err
	}
	started := false
	for {
		images, err := api.catalog(ctx)
		if err != nil && !transient(err) {
			return "", fmt.Errorf("read the image catalog: %w", err)
		}
		if err == nil {
			img, ok := pickImage(images, imageFamilies[recipe.Image])
			if !ok {
				return "", fmt.Errorf("this host's image catalog has no %s image", recipe.Image)
			}
			switch img.Status {
			case "cached":
				return img.ID, nil
			case "pulling":
				m.mu.Lock()
				m.progress[inst.Name] = img.Progress
				m.mu.Unlock()
			default:
				if started {
					if st, serr := api.pullStatus(ctx, img.ID); serr == nil && st.Error != "" {
						return "", fmt.Errorf("download %s: %s", img.ID, st.Error)
					}
				}
				// A 409 means another pull got there first; the next look
				// sees it as pulling or cached.
				var ae *apiError
				perr := api.pull(ctx, img.ID)
				raced := errors.As(perr, &ae) && ae.Status == http.StatusConflict
				if perr != nil && !raced {
					return "", fmt.Errorf("download %s: %w", img.ID, perr)
				}
				started = true
			}
		}
		if err := m.sleep(ctx); err != nil {
			return "", err
		}
	}
}

func pickImage(images []catalogImage, ids []string) (catalogImage, bool) {
	for _, img := range images {
		for _, id := range ids {
			if img.ID == id {
				return img, true
			}
		}
	}
	return catalogImage{}, false
}

func (m *Manager) createVM(ctx context.Context, inst Instance, recipe Recipe) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	var vms map[string]vmView
	for {
		var lerr error
		vms, lerr = api.vms(ctx)
		if lerr == nil {
			break
		}
		if !transient(lerr) {
			return fmt.Errorf("list VMs: %w", lerr)
		}
		if serr := m.sleep(ctx); serr != nil {
			return serr
		}
	}

	size, _ := recipe.Size(inst.Size)
	for _, mem := range inst.members() {
		if _, exists := vms[mem.VM]; exists {
			continue // made before a restart interrupted this step
		}
		role, ok := recipe.role(mem.Role)
		if !ok {
			return fmt.Errorf("%s has no %q machines", recipe.Name, mem.Role)
		}
		script, err := installScript(recipe, role, inst)
		if err != nil {
			return err
		}
		req := vmCreate{
			Name:     mem.VM,
			ImageID:  inst.ImageID,
			VCPUs:    size.VCPUs,
			MemoryMB: size.MemoryMB,
			DiskGB:   size.DiskGB,
			Service:  inst.Name,
			CloudInit: vmCreateCloud{
				Username:     serviceUser,
				Password:     inst.VMPassword,
				SSHPublicKey: m.pubKey,
				GitHubUser:   inst.GitHubUser,
				WriteFiles:   []vmCreateFile{{Path: installPath, Content: script, Permissions: "0700"}},
				RunCmd:       []string{installPath},
			},
		}
		if err := api.createVM(ctx, req); err != nil {
			if len(inst.VMs) > 1 {
				return fmt.Errorf("create the VM %s: %w", mem.VM, err)
			}
			return fmt.Errorf("create the VM: %w", err)
		}
	}
	return nil
}

// envFile is where a role that waits for another is handed its Env.
const envFile = "/var/lib/nanocloud/env"

// installScript wraps a role's install body in the contract the manager
// reads back: the state file says "installing", then "ready" or "failed: …",
// and everything the script prints goes to the log. A role that waits for
// another first says "waiting" and blocks until the daemon hands it its Env.
func installScript(recipe Recipe, role Role, inst Instance) (string, error) {
	body, err := render(role.Install, templateValues(inst, nil))
	if err != nil {
		return "", fmt.Errorf("render the %s install script: %w", recipe.Name, err)
	}
	what := recipe.Name
	if len(recipe.Roles) > 0 {
		what = recipe.Name + " (" + strings.ToLower(role.Label) + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `#!/bin/bash
# Sets up the %s service %s. Written by nano-cloud, which follows its progress
# through %s; the full output is in %s.
set -Eeuo pipefail
mkdir -p %s
exec >>%s 2>&1
trap 'echo "failed: $BASH_COMMAND (line $LINENO)" > %s' ERR
# fail stops the install with a reason nano-cloud shows as it is.
fail() { echo "$*"; echo "failed because: $*" > %s; exit 1; }
`, what, inst.Name, stateFile, logFile, shellQuote("/var/lib/nanocloud"), logFile, stateFile, stateFile)
	if role.After != "" {
		fmt.Fprintf(&b, "echo waiting > %s\n", stateFile)
		fmt.Fprintf(&b, "until [ -s %s ]; do sleep 3; done\n", envFile)
		fmt.Fprintf(&b, "set -a; . %s; set +a\n", envFile)
	}
	fmt.Fprintf(&b, "echo installing > %s\n", stateFile)
	b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
	if len(inst.SSHKeys) > 0 {
		home := "/home/" + serviceUser
		fmt.Fprintf(&b, "install -d -m 700 -o %[1]s -g %[1]s %[2]s/.ssh\n", serviceUser, home)
		fmt.Fprintf(&b, "cat >> %s/.ssh/authorized_keys <<'NANOCLOUD_KEYS'\n", home)
		for _, k := range inst.SSHKeys {
			b.WriteString(k + "\n")
		}
		b.WriteString("NANOCLOUD_KEYS\n")
		fmt.Fprintf(&b, "chown %[1]s:%[1]s %[2]s/.ssh/authorized_keys\n", serviceUser, home)
	}
	b.WriteString(strings.TrimRight(body, "\n") + "\n")
	fmt.Fprintf(&b, "echo ready > %s\n", stateFile)
	return b.String(), nil
}

func (m *Manager) waitForNetwork(ctx context.Context, inst Instance) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	for {
		vms, err := api.vms(ctx)
		if err != nil && !transient(err) {
			return fmt.Errorf("list VMs: %w", err)
		}
		if err == nil {
			up := 0
			for _, mem := range inst.members() {
				vm, ok := vms[mem.VM]
				if !ok {
					return fmt.Errorf("its VM %q no longer exists", mem.VM)
				}
				switch vm.Phase {
				case "ready":
					up++
				case "gave-up", "crash-looping":
					return fmt.Errorf("its VM %q keeps stopping on its own; open the VM to see why", vm.ID)
				case "no-network":
					// Also measured from the last retry, so a VM that was
					// just fixed gets the full wait.
					sinceRetry := time.Since(time.Unix(inst.RetriedAtUnix, 0))
					if time.Duration(vm.Uptime)*time.Second > m.noNetworkAfter && sinceRetry > m.noNetworkAfter {
						return fmt.Errorf("its VM %q got no network address in %s", vm.ID, m.noNetworkAfter)
					}
				}
			}
			if up == len(inst.members()) {
				return nil
			}
		}
		if err := m.sleep(ctx); err != nil {
			return err
		}
	}
}

// probeCmd reads the install's state, or, before the script has started,
// cloud-init's — "done" with no state file means the script never ran.
const probeCmd = "cat " + stateFile + " 2>/dev/null || { printf 'cloud-init '; cloud-init status 2>/dev/null | head -n 1; }"

func (m *Manager) waitForInstall(ctx context.Context, inst Instance, recipe Recipe) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	var up time.Duration
	last := time.Now()
	for {
		now := time.Now()
		vms, err := api.vms(ctx)
		if err != nil && !transient(err) {
			return fmt.Errorf("list VMs: %w", err)
		}
		if err == nil {
			done, allUp, err := m.lookAtInstall(ctx, &inst, recipe, vms)
			if done || err != nil {
				return err
			}
			// The clock only runs while every VM is up, so stopping a VM
			// pauses the timeout.
			if allUp {
				up += now.Sub(last)
				if up > m.installTimeout {
					return fmt.Errorf("the install did not finish within %s; its log is %s on the VM", m.installTimeout, logFile)
				}
			}
		}
		last = now
		if err := m.sleep(ctx); err != nil {
			return err
		}
	}
}

// lookAtInstall checks every VM's install once. It reports done when the
// service is ready and an error when an install failed. It also hands each
// waiting role its Env once the role it waits for is ready.
func (m *Manager) lookAtInstall(ctx context.Context, inst *Instance, recipe Recipe, vms map[string]vmView) (done, allUp bool, err error) {
	members := inst.members()
	states := map[string]string{}
	allUp = true
	// Check every VM before reporting a failure: workers that cannot join
	// usually fail together, and the console shows each one.
	var failed []*installFailure
	for _, mem := range members {
		vm, ok := vms[mem.VM]
		if !ok {
			return false, false, fmt.Errorf("its VM %q no longer exists", mem.VM)
		}
		if vm.Phase != "ready" || vm.IP == "" {
			allUp = false
			continue
		}
		state, f := m.installState(ctx, inst, mem, vm.IP, len(members) > 1)
		if f != nil {
			failed = append(failed, f)
			continue
		}
		states[mem.VM] = state
	}
	if len(failed) > 0 {
		return false, false, installsFailed(failed)
	}

	// A role is ready when each of its VMs says so and answers on the role's
	// ports from the host.
	ready := map[string]bool{}
	for _, role := range recipe.roles() {
		ready[role.ID] = true
		for _, mem := range members {
			if mem.Role != role.ID {
				continue
			}
			if states[mem.VM] != "ready" || !m.portsOpen(ctx, vms[mem.VM].IP, role.Ports) {
				ready[role.ID] = false
			}
		}
	}

	ips := roleIPs(*inst, vms)
	for _, role := range recipe.roles() {
		if role.After == "" || !ready[role.After] {
			continue
		}
		for _, mem := range members {
			if mem.Role == role.ID && states[mem.VM] == "waiting" {
				// A failed hand-over is tried again on the next look.
				if m.handOver(ctx, *inst, role, mem.VM, vms[mem.VM].IP, ips) == nil {
					waitedFor, _ := recipe.role(role.After)
					m.note(inst.Name, fmt.Sprintf("Handed the %s's address to %s.", strings.ToLower(waitedFor.Label), mem.VM))
				}
			}
		}
	}

	for _, role := range recipe.roles() {
		if !ready[role.ID] {
			return false, allUp, nil
		}
	}
	if recipe.ReadyCheck == "" {
		return true, allUp, nil
	}
	check, err := render(recipe.ReadyCheck, templateValues(*inst, ips))
	if err != nil {
		return false, allUp, err
	}
	primary := members[0]
	if _, _, err := m.guest.Run(ctx, vms[primary.VM].IP, inst.hostKey(primary.VM), check); err != nil {
		return false, allUp, nil // not there yet
	}
	return true, allUp, nil
}

// installState reads one VM's install state: "installing", "waiting",
// "ready", or "" while it cannot be reached. An install that failed or never
// ran is returned as a failure.
func (m *Manager) installState(ctx context.Context, inst *Instance, mem Member, ip string, many bool) (string, *installFailure) {
	failed := func(why string) *installFailure {
		// The console shows the first line beside the VM; the log lines
		// after it go in the service's banner.
		first, _, _ := strings.Cut(why, "\n")
		m.lookFailed(inst.Name, mem.VM, first)
		return &installFailure{vm: mem.VM, reason: why, many: many}
	}
	out, seen, err := m.guest.Run(ctx, ip, inst.hostKey(mem.VM), probeCmd)
	m.observe(inst.Name, mem.VM, ip, strings.TrimSpace(out), err)
	if inst.hostKey(mem.VM) == "" && seen != "" {
		if uerr := m.store.Update(inst.Name, func(i *Instance) {
			if i.HostKeys == nil {
				i.HostKeys = map[string]string{}
			}
			i.HostKeys[mem.VM] = seen
		}); uerr == nil {
			if inst.HostKeys == nil {
				inst.HostKeys = map[string]string{}
			}
			inst.HostKeys[mem.VM] = seen
		}
	}
	if errors.Is(err, ErrHostKeyChanged) {
		return "", failed("the VM presented a different SSH host key from the one first seen; delete and recreate the service if the VM was rebuilt")
	}
	if err != nil {
		return "", nil // sshd not up yet, or a blip: look again
	}
	state := strings.TrimSpace(out)
	switch {
	case strings.HasPrefix(state, "failed because:"):
		// The install said why itself; its log's end shows what led there.
		reason := strings.TrimSpace(strings.TrimPrefix(state, "failed because:"))
		if tail, _, terr := m.guest.Run(ctx, ip, inst.hostKey(mem.VM), "tail -n 9 "+logFile); terr == nil {
			// fail() logged the reason too, last: say it once.
			tail = strings.TrimSuffix(strings.TrimSpace(tail), reason)
			if tail = strings.TrimSpace(tail); tail != "" {
				reason += "\n" + tail
			}
		}
		return "", failed(reason)
	case strings.HasPrefix(state, "failed"):
		reason := strings.TrimSpace(strings.TrimPrefix(state, "failed:"))
		if tail, _, terr := m.guest.Run(ctx, ip, inst.hostKey(mem.VM), "tail -n 5 "+logFile); terr == nil && strings.TrimSpace(tail) != "" {
			reason += "\n" + strings.TrimSpace(tail)
		}
		return "", failed("the install failed at " + reason)
	case strings.HasPrefix(state, "cloud-init status: done"), strings.HasPrefix(state, "cloud-init status: error"):
		return "", failed("cloud-init finished without running the install script")
	case state == "ready", state == "waiting", state == "installing":
		return state, nil
	}
	return "", nil
}

// installFailure is a VM whose install failed, and why. With many set the
// message starts "on <vm>, " rather than with the VM name, which sentence
// would capitalise.
type installFailure struct {
	vm, reason string
	many       bool
}

func (f *installFailure) Error() string {
	if f.many {
		return "on " + f.vm + ", " + f.reason
	}
	return f.reason
}

// installsFailed reports the first failure in full, then each other failed
// VM, briefly when it failed the same way.
func installsFailed(failed []*installFailure) error {
	if len(failed) == 1 {
		return failed[0]
	}
	msg := failed[0].Error()
	first, _, tail := strings.Cut(failed[0].reason, "\n")
	sep := "\n\n" // after the first failure's log lines
	if !tail {
		msg = strings.TrimSuffix(msg, ".") + "."
		sep = "\n"
	}
	for _, f := range failed[1:] {
		if why, _, _ := strings.Cut(f.reason, "\n"); why == first {
			msg += sep + f.vm + " failed the same way."
		} else {
			msg += sep + sentence("on "+f.vm+", "+why)
		}
		sep = "\n"
	}
	return errors.New(msg)
}

func (m *Manager) portsOpen(ctx context.Context, ip string, ports []int) bool {
	for _, port := range ports {
		if m.dial(ctx, net.JoinHostPort(ip, strconv.Itoa(port))) != nil {
			return false
		}
	}
	return true
}

// handOver writes a waiting VM's Env once the role it waits for has
// addresses. It writes a temporary file and renames it, so the script never
// reads half of it.
func (m *Manager) handOver(ctx context.Context, inst Instance, role Role, vm, ip string, ips map[string]string) error {
	values := templateValues(inst, ips)
	keys := make([]string, 0, len(role.Env))
	for k := range role.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v, err := render(role.Env[k], values)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s=%s\n", k, shellQuote(v))
	}
	enc := base64.StdEncoding.EncodeToString([]byte(b.String()))
	cmd := "echo " + enc + " | base64 -d | sudo -n install -m 600 /dev/stdin " + envFile + ".tmp && sudo -n mv " + envFile + ".tmp " + envFile
	_, _, err := m.guest.Run(ctx, ip, inst.hostKey(vm), cmd)
	return err
}

const (
	charsetAlnum = "alnum"
	charsetUpper = "upper"
)

// randomString returns n characters from crypto/rand. Letters and digits
// only, so a value is safe unquoted in a URL, a shell word or SQL.
func randomString(n int, charset string) (string, error) {
	letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	if charset == charsetUpper {
		letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}
	max := big.NewInt(int64(len(letters)))
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("services: random: %w", err)
		}
		b[i] = letters[k.Int64()]
	}
	return string(b), nil
}

// sentence makes an error message read as a sentence in the UI.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") && !strings.Contains(s, "\n") {
		s += "."
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
