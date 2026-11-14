package services

import (
	"context"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The manager's side of the gateway: host port allocation, what the gateway
// listens for, where connections go, and how addresses read to the viewer.

// The gateway allocates host ports from gatewayPortBase, gatewayPortSpan of
// them.
const (
	gatewayPortBase = 30000
	gatewayPortSpan = 10000
)

// EndpointView is one of a service's endpoints and where it can be reached.
type EndpointView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Scheme string `json:"scheme,omitempty"`
	// Port is the endpoint's port on its VM; HostPort is the gateway's.
	Port     int `json:"port"`
	HostPort int `json:"host_port,omitempty"`
	// Address is host:port through the gateway, as the viewer reaches it;
	// Direct is the VM's own address, once it has one.
	Address string `json:"address,omitempty"`
	Direct  string `json:"direct,omitempty"`
	// Problem says why the gateway is not forwarding the endpoint.
	Problem string `json:"problem,omitempty"`
	// Published marks a NodePort a person published and may unpublish.
	Published bool `json:"published,omitempty"`
}

// portCandidates lists the host ports to try for an endpoint on port p, best
// first: base + p%1000 (5432 becomes 30432), then the same offset in each
// further thousand, then any.
func portCandidates(base, span, p int) []int {
	out := make([]int, 0, span)
	seen := make(map[int]bool, span)
	add := func(c int) {
		if c >= base && c < base+span && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for k := 0; k*1000 < span; k++ {
		add(base + k*1000 + p%1000)
	}
	for c := base; c < base+span; c++ {
		add(c)
	}
	return out
}

// takenPorts returns every host port a service holds.
func takenPorts(list []Instance) map[int]bool {
	taken := map[int]bool{}
	for _, inst := range list {
		for _, p := range inst.Ports {
			taken[p] = true
		}
	}
	return taken
}

// endpointsOf is every endpoint the gateway forwards for a service: its
// recipe's, then the NodePorts a person published.
func endpointsOf(inst Instance, recipe Recipe) []Endpoint {
	out := append([]Endpoint(nil), recipe.Endpoints...)
	for _, p := range inst.Published {
		out = append(out, Endpoint{ID: publishedID(p.NodePort), Label: p.Label, Port: p.NodePort, Scheme: p.Scheme, anyNode: true})
	}
	return out
}

// allocatePorts returns have plus a host port for each endpoint that lacks
// one, marking new ones taken. An endpoint left without a port is reachable
// on its VM only.
func (m *Manager) allocatePorts(endpoints []Endpoint, have map[string]int, taken map[int]bool) map[string]int {
	out := make(map[string]int, len(endpoints))
	for id, p := range have {
		out[id] = p
	}
	for _, e := range endpoints {
		if out[e.ID] > 0 {
			continue
		}
		for _, c := range portCandidates(m.portBase, m.portSpan, e.Port) {
			if !taken[c] && m.portFree(c) {
				out[e.ID] = c
				taken[c] = true
				break
			}
		}
		if out[e.ID] == 0 {
			log.Printf("services: no free gateway port for %s; it is reachable on its VM only", e.Label)
		}
	}
	return out
}

// portFree reports whether nothing on this host listens on port.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	ln.Close() //nolint:errcheck // only the bind mattered
	return true
}

// backfillPorts gives ports to the endpoints of services stored before they
// had any, or before their recipe gained the endpoint.
func (m *Manager) backfillPorts() {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	list, err := m.store.List()
	if err != nil {
		return
	}
	taken := takenPorts(list)
	for _, inst := range list {
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			continue
		}
		ports := m.allocatePorts(endpointsOf(inst, recipe), inst.Ports, taken)
		if len(ports) == len(inst.Ports) {
			continue
		}
		if err := m.store.Update(inst.Name, func(i *Instance) { i.Ports = ports }); err != nil {
			log.Printf("services: %s: record gateway ports: %v", inst.Name, err)
		}
	}
}

// syncGateway makes the gateway listen for every endpoint of every stored
// service, honouring each service's Shared setting.
func (m *Manager) syncGateway() {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	list, err := m.store.List()
	if err != nil {
		log.Printf("services: gateway: cannot read services: %v", err)
		return
	}
	var want []binding
	for _, inst := range list {
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			continue
		}
		for _, e := range endpointsOf(inst, recipe) {
			if p := inst.Ports[e.ID]; p > 0 {
				want = append(want, binding{service: inst.Name, endpoint: e.ID, port: p, shared: inst.Shared, scheme: e.Scheme})
			}
		}
	}
	m.gw.sync(want)
}

// keepGateway re-syncs the gateway periodically, so an endpoint whose port
// another program held is picked up once it is released.
func (m *Manager) keepGateway() {
	defer m.wg.Done()
	t := time.NewTicker(m.gatewayRetry)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.syncGateway()
		}
	}
}

// resolveEndpoint returns where a gateway connection goes: the first ready
// VM of the endpoint's role (any role for a published NodePort), or why there
// is none.
func (m *Manager) resolveEndpoint(ctx context.Context, service, endpoint string) (string, error) {
	inst, err := m.store.Get(service)
	if err != nil {
		return "", fmt.Errorf("%s no longer exists", service)
	}
	recipe, _ := RecipeByID(inst.Recipe)
	var e Endpoint
	ok := false
	for _, c := range endpointsOf(inst, recipe) {
		if c.ID == endpoint {
			e, ok = c, true
		}
	}
	if !ok {
		return "", fmt.Errorf("%s has no endpoint %q", service, endpoint)
	}
	vms, err := m.recentVMs(ctx)
	if err != nil {
		return "", fmt.Errorf("nano-cloud cannot see %s's VMs right now", service)
	}
	found, running := false, false
	for _, mem := range inst.members() {
		vm, ok := vms[mem.VM]
		if (!e.anyNode && mem.Role != e.role()) || !ok {
			continue
		}
		if vm.Phase == "ready" && vm.IP != "" {
			return net.JoinHostPort(vm.IP, strconv.Itoa(e.Port)), nil
		}
		found, running = true, running || vm.Status == "running"
	}
	if !found {
		return "", &downError{why: service + "'s VM no longer exists", todo: "Open the service in nano-cloud to see what to do."}
	}
	if running {
		return "", fmt.Errorf("%s is still starting", service)
	}
	return "", &downError{why: service + " is stopped", todo: "Start it in nano-cloud, then reload this page."}
}

// recentVMs lists the VMs, reusing a very recent listing, because one Grafana
// page opens dozens of connections at once.
func (m *Manager) recentVMs(ctx context.Context) (map[string]vmView, error) {
	m.vmMu.Lock()
	defer m.vmMu.Unlock()
	if m.vmCache != nil && time.Since(m.vmCacheAt) < m.vmCacheTTL {
		return m.vmCache, nil
	}
	api, err := m.client()
	if err != nil {
		return nil, err
	}
	vms, err := api.vms(ctx)
	if err != nil {
		return nil, err
	}
	m.vmCache, m.vmCacheAt = vms, time.Now()
	return vms, nil
}

// SetShared opens a service's endpoints to other machines, or closes them
// to all but this host again.
func (m *Manager) SetShared(ctx context.Context, name string, shared bool) (View, error) {
	if err := m.store.Update(name, func(i *Instance) { i.Shared = shared }); err != nil {
		return View{}, err
	}
	m.syncGateway()
	return m.Get(ctx, name)
}

type viewerKey struct{}

// ViewedFrom records the request's Host header, so a shared service's
// addresses use that host for viewers on other machines. Local viewers see
// localhost.
func ViewedFrom(ctx context.Context, hostport string) context.Context {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return context.WithValue(ctx, viewerKey{}, strings.Trim(host, "[]"))
}

// hostNamePattern accepts a host name or IP address, and nothing that could
// alter a command it is put in.
var hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)

// gatewayHost is the host shown in gateway addresses for the viewer in ctx.
func gatewayHost(ctx context.Context, inst Instance) string {
	host, _ := ctx.Value(viewerKey{}).(string)
	if !inst.Shared || !hostNamePattern.MatchString(host) || isLocalName(host) {
		return "localhost"
	}
	return host
}

func isLocalName(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// endpointViews are a service's endpoints as the viewer in ctx reaches them.
// ips is roleIPs' map.
func (m *Manager) endpointViews(ctx context.Context, inst Instance, recipe Recipe, ips map[string]string) []EndpointView {
	host := gatewayHost(ctx, inst)
	all := endpointsOf(inst, recipe)
	out := make([]EndpointView, 0, len(all))
	for _, e := range all {
		ev := EndpointView{ID: e.ID, Label: e.Label, Scheme: e.Scheme, Port: e.Port, HostPort: inst.Ports[e.ID], Published: e.anyNode}
		ip := ips[e.role()]
		if e.anyNode {
			ip = ips[""]
		}
		if ip != "" {
			ev.Direct = net.JoinHostPort(ip, strconv.Itoa(e.Port))
		}
		listening, problem := m.gw.status(inst.Name, e.ID)
		switch {
		case ev.HostPort == 0:
			ev.Problem = "No free port on this host to forward it from."
		case listening:
			ev.Address = net.JoinHostPort(host, strconv.Itoa(ev.HostPort))
		default:
			ev.Problem = problem
		}
		out = append(out, ev)
	}
	return out
}

// endpointValues are each endpoint's addr_, host_ and port_ template values:
// through the gateway while it forwards, otherwise at the VM.
func endpointValues(views []EndpointView) map[string]string {
	values := make(map[string]string, 3*len(views))
	for _, ev := range views {
		addr := ev.Address
		if addr == "" {
			addr = ev.Direct
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		values["addr_"+ev.ID], values["host_"+ev.ID], values["port_"+ev.ID] = addr, host, port
	}
	return values
}
