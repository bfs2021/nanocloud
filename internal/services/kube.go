package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// Console support for Kubernetes services: read nodes and workloads, scale
// workers, apply manifests and publish NodePorts. Everything runs as kubectl
// on the control plane over SSH.

const consoleKubernetes = "kubernetes"

// ClusterView is a Kubernetes service's cluster as it is now.
type ClusterView struct {
	Version     string        `json:"version"`
	Nodes       []NodeView    `json:"nodes"`
	Deployments []DeployView  `json:"deployments"`
	Exposed     []ExposedView `json:"exposed"`
	Pods        PodCounts     `json:"pods"`
	ReadAtUnix  int64         `json:"read_at_unix"`
}

// NodeView is one node. CPU and memory are as metrics-server last saw them,
// absent before it has.
type NodeView struct {
	Name          string   `json:"name"`
	Roles         []string `json:"roles"`
	Ready         bool     `json:"ready"`
	Unschedulable bool     `json:"unschedulable,omitempty"`
	Version       string   `json:"version"`
	IP            string   `json:"ip,omitempty"`
	Pods          int      `json:"pods"`
	CPUPercent    *int     `json:"cpu_percent,omitempty"`
	MemoryPercent *int     `json:"memory_percent,omitempty"`
}

// DeployView is one deployment.
type DeployView struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Ready       int      `json:"ready"`
	Desired     int      `json:"desired"`
	Images      []string `json:"images"`
	CreatedUnix int64    `json:"created_unix,omitempty"`
}

// ExposedView is a Kubernetes service reachable from outside the cluster:
// a NodePort or LoadBalancer one.
type ExposedView struct {
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Ports     []PortView `json:"ports"`
}

// PortView is one port of an exposed service.
type PortView struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	NodePort int    `json:"node_port,omitempty"`
	Protocol string `json:"protocol"`
}

// PodCounts counts the cluster's pods by phase.
type PodCounts struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Failed    int `json:"failed"`
	Succeeded int `json:"succeeded"`
}

// clusterTTL is how long a cluster reading is reused; the console polls and
// each reading is an SSH round trip.
const clusterTTL = 5 * time.Second

type clusterReading struct {
	at   time.Time
	view ClusterView
}

// topMarker separates kubectl get's JSON from kubectl top's table in one
// reading.
const topMarker = "----nanocloud-top----"

// clusterCmd reads everything the console shows in one round trip. top
// fails until metrics-server has data, so its failure is ignored.
const clusterCmd = "sudo -n k3s kubectl get nodes,deployments,services,pods -A -o json && echo " + topMarker +
	" && (sudo -n k3s kubectl top nodes --no-headers 2>/dev/null || true)"

// Cluster reads a Kubernetes service's cluster, or returns a reading a few
// seconds old.
func (m *Manager) Cluster(ctx context.Context, name string) (ClusterView, error) {
	inst, recipe, err := m.kubeService(name)
	if err != nil {
		return ClusterView{}, err
	}
	m.kubeMu.Lock()
	if r, ok := m.clusters[name]; ok && time.Since(r.at) < clusterTTL {
		m.kubeMu.Unlock()
		return r.view, nil
	}
	m.kubeMu.Unlock()

	out, err := m.onControlPlane(ctx, inst, recipe, clusterCmd)
	if err != nil {
		return ClusterView{}, err
	}
	view, err := parseCluster(out)
	if err != nil {
		return ClusterView{}, fmt.Errorf("read %s's cluster: %w", name, err)
	}
	view.ReadAtUnix = time.Now().Unix()
	m.kubeMu.Lock()
	m.clusters[name] = clusterReading{at: time.Now(), view: view}
	m.kubeMu.Unlock()
	return view, nil
}

// forgetCluster drops a service's cached reading after a change to it.
func (m *Manager) forgetCluster(name string) {
	m.kubeMu.Lock()
	delete(m.clusters, name)
	m.kubeMu.Unlock()
}

// kubeService returns a service that has the Kubernetes console.
func (m *Manager) kubeService(name string) (Instance, Recipe, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return Instance{}, Recipe{}, err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	if recipe.Console != consoleKubernetes {
		return Instance{}, Recipe{}, errorOf(ErrInvalid, "%s is not a Kubernetes service", name)
	}
	return inst, recipe, nil
}

// onControlPlane runs cmd on the service's first VM, its control plane.
func (m *Manager) onControlPlane(ctx context.Context, inst Instance, recipe Recipe, cmd string) (string, error) {
	vms, err := m.recentVMs(ctx)
	if err != nil {
		return "", listError(err)
	}
	cp := ""
	for _, mem := range inst.members() {
		if mem.Role == recipe.roles()[0].ID {
			cp = mem.VM
			break
		}
	}
	v, ok := vms[cp]
	if !ok || v.Phase != "ready" || v.IP == "" {
		return "", errorOf(ErrNotReady, "%s's control plane is not up", inst.Name)
	}
	out, _, err := m.guest.Run(ctx, v.IP, inst.hostKey(cp), cmd)
	return out, err
}

// kubeList is the part of kubectl's JSON the console reads, for every kind
// it asks for at once.
type kubeList struct {
	Items []struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name              string            `json:"name"`
			Namespace         string            `json:"namespace"`
			Labels            map[string]string `json:"labels"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Unschedulable bool   `json:"unschedulable"`
			NodeName      string `json:"nodeName"`
			Replicas      *int   `json:"replicas"`
			Type          string `json:"type"`
			Ports         []struct {
				Name     string `json:"name"`
				Port     int    `json:"port"`
				NodePort int    `json:"nodePort"`
				Protocol string `json:"protocol"`
			} `json:"ports"`
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			Phase         string `json:"phase"`
			ReadyReplicas int    `json:"readyReplicas"`
			Addresses     []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
		} `json:"status"`
	} `json:"items"`
}

// parseCluster turns clusterCmd's output into a ClusterView.
func parseCluster(out string) (ClusterView, error) {
	getJSON, top, _ := strings.Cut(out, topMarker)
	var list kubeList
	if err := json.Unmarshal([]byte(getJSON), &list); err != nil {
		return ClusterView{}, fmt.Errorf("kubectl gave no JSON: %w", err)
	}
	view := ClusterView{Nodes: []NodeView{}, Deployments: []DeployView{}, Exposed: []ExposedView{}}
	podsOn := map[string]int{}
	for _, it := range list.Items {
		switch it.Kind {
		case "Node":
			n := NodeView{Name: it.Metadata.Name, Roles: []string{}, Unschedulable: it.Spec.Unschedulable, Version: it.Status.NodeInfo.KubeletVersion}
			for label := range it.Metadata.Labels {
				if role, ok := strings.CutPrefix(label, "node-role.kubernetes.io/"); ok && role != "master" {
					n.Roles = append(n.Roles, role)
				}
			}
			for _, a := range it.Status.Addresses {
				if a.Type == "InternalIP" {
					n.IP = a.Address
				}
			}
			for _, c := range it.Status.Conditions {
				if c.Type == "Ready" {
					n.Ready = c.Status == "True"
				}
			}
			view.Nodes = append(view.Nodes, n)
		case "Deployment":
			d := DeployView{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Ready: it.Status.ReadyReplicas, Desired: 1, Images: []string{}}
			if it.Spec.Replicas != nil {
				d.Desired = *it.Spec.Replicas
			}
			if !it.Metadata.CreationTimestamp.IsZero() {
				d.CreatedUnix = it.Metadata.CreationTimestamp.Unix()
			}
			for _, c := range it.Spec.Template.Spec.Containers {
				d.Images = append(d.Images, c.Image)
			}
			view.Deployments = append(view.Deployments, d)
		case "Service":
			if it.Spec.Type != "NodePort" && it.Spec.Type != "LoadBalancer" {
				continue
			}
			e := ExposedView{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Type: it.Spec.Type, Ports: []PortView{}}
			for _, p := range it.Spec.Ports {
				e.Ports = append(e.Ports, PortView{Name: p.Name, Port: p.Port, NodePort: p.NodePort, Protocol: p.Protocol})
			}
			view.Exposed = append(view.Exposed, e)
		case "Pod":
			view.Pods.Total++
			switch it.Status.Phase {
			case "Running":
				view.Pods.Running++
				podsOn[it.Spec.NodeName]++
			case "Pending":
				view.Pods.Pending++
			case "Failed":
				view.Pods.Failed++
			case "Succeeded":
				view.Pods.Succeeded++
			}
		}
	}
	usage := parseTop(top)
	for i := range view.Nodes {
		n := &view.Nodes[i]
		n.Pods = podsOn[n.Name]
		if u, ok := usage[n.Name]; ok {
			n.CPUPercent, n.MemoryPercent = &u[0], &u[1]
		}
		// The version a person asks about is the control plane's.
		if view.Version == "" || hasRole(n.Roles, "control-plane") {
			view.Version = n.Version
		}
	}
	return view, nil
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// parseTop reads `kubectl top nodes --no-headers`: name, CPU, CPU%, memory,
// memory%. Lines it cannot read are skipped.
func parseTop(top string) map[string][2]int {
	out := map[string][2]int{}
	for _, line := range strings.Split(top, "\n") {
		f := strings.Fields(line)
		if len(f) != 5 {
			continue
		}
		cpu, err1 := strconv.Atoi(strings.TrimSuffix(f[2], "%"))
		mem, err2 := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
		if err1 == nil && err2 == nil {
			out[f[0]] = [2]int{cpu, mem}
		}
	}
	return out
}

// ScaleView says which machines of a service a person may add or remove,
// and how many there are.
type ScaleView struct {
	Role  string `json:"role"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// scaleView is the service's scalable role, if it has one.
func scaleView(inst Instance, recipe Recipe) *ScaleView {
	role, ok := recipe.scalableRole()
	if !ok {
		return nil
	}
	n := 0
	for _, mem := range inst.members() {
		if mem.Role == role.ID {
			n++
		}
	}
	return &ScaleView{Role: role.ID, Label: role.Label, Count: n}
}

// busy says why a service cannot take a change now, or "".
func busy(inst Instance) string {
	switch {
	case len(inst.Removing) > 0:
		return fmt.Sprintf("%s is removing %s", inst.Name, strings.Join(inst.Removing, ", "))
	case inst.State != StateReady:
		return fmt.Sprintf("%s is not ready", inst.Name)
	}
	return ""
}

// checkCount checks a new number of a role's machines against the
// recipe's own limits for it.
func checkCount(recipe Recipe, role Role, n int) error {
	p, _ := recipe.param(role.CountParam)
	if p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(strconv.Itoa(n)) {
		msg := fmt.Sprintf("%s takes %s %ss, not %d", recipe.Name, strings.TrimPrefix(p.Hint, "a number "), strings.ToLower(role.Label), n)
		if p.Hint == "" {
			msg = fmt.Sprintf("%s cannot have %d %ss", recipe.Name, n, strings.ToLower(role.Label))
		}
		return errorOf(ErrInvalid, "%s", msg)
	}
	return nil
}

// nextNames names n more machines of a role, numbered on from the highest
// there is: a role whose only machine is lab-agent gets lab-agent-2 next.
func nextNames(inst Instance, role Role, n int) []string {
	base := inst.Name + "-" + strings.ReplaceAll(role.ID, "_", "-")
	highest := 0
	for _, mem := range inst.members() {
		if mem.Role != role.ID {
			continue
		}
		if mem.VM == base {
			highest = max(highest, 1)
		} else if k, err := strconv.Atoi(strings.TrimPrefix(mem.VM, base+"-")); err == nil {
			highest = max(highest, k)
		}
	}
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%d", base, highest+1+i)
	}
	return names
}

// AddWorkers adds n machines to a running service's scalable role. They go
// through the normal build steps while the cluster keeps serving.
func (m *Manager) AddWorkers(ctx context.Context, name string, n int) (View, error) {
	if n < 1 || n > 5 {
		return View{}, errorOf(ErrInvalid, "add between 1 and 5 machines at a time")
	}
	api, err := m.client()
	if err != nil {
		return View{}, err
	}
	inst, err := m.store.Get(name)
	if err != nil {
		return View{}, err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	role, ok := recipe.scalableRole()
	if !ok {
		return View{}, errorOf(ErrInvalid, "%s has a fixed set of machines", name)
	}
	if why := busy(inst); why != "" {
		return View{}, errorOf(ErrNotReady, "%s; try again once it is", why)
	}
	count := scaleView(inst, recipe).Count
	if err = checkCount(recipe, role, count+n); err != nil {
		return View{}, err
	}
	names := nextNames(inst, role, n)
	vms, err := api.vms(ctx)
	if err != nil {
		return View{}, listError(err)
	}
	for _, vm := range names {
		if len(vm) > 63 {
			return View{}, errorOf(ErrInvalid, "%s's name is too long for more machines", name)
		}
		if _, taken := vms[vm]; taken {
			return View{}, errorOf(ErrExists, "a VM named %q already exists", vm)
		}
	}
	if err := m.store.Update(name, func(i *Instance) {
		for _, vm := range names {
			i.Members = append(i.members(), Member{Role: role.ID, VM: vm})
			i.VMs = append(i.VMs, vm)
			// A name used before is a new VM: its exporters are not set up.
			unwatch(i, vm)
		}
		i.Params[role.CountParam] = strconv.Itoa(count + n)
		i.Adding = names
		i.State = StateCreating
	}); err != nil {
		return View{}, err
	}
	m.note(name, fmt.Sprintf("Adding %s to %s.", strings.Join(names, ", "), name))
	m.forgetCluster(name)
	m.startWorker(name)
	return m.Get(ctx, name)
}

// drainTimeout bounds moving a node's pods away before it is removed.
const drainTimeout = 3 * time.Minute

// RemoveWorker takes one machine out of a running service's scalable role.
// On Kubernetes the node is drained and deleted before its VM. It returns at
// once; the removal runs in the background and resumes after a restart.
func (m *Manager) RemoveWorker(ctx context.Context, name, vm string) (View, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return View{}, err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	role, ok := recipe.scalableRole()
	if !ok {
		return View{}, errorOf(ErrInvalid, "%s has a fixed set of machines", name)
	}
	member := false
	for _, mem := range inst.members() {
		member = member || (mem.VM == vm && mem.Role == role.ID)
	}
	if !member {
		return View{}, errorOf(ErrInvalid, "%s is not one of %s's %ss", vm, name, strings.ToLower(role.Label))
	}
	if why := busy(inst); why != "" {
		return View{}, errorOf(ErrNotReady, "%s; try again once it is", why)
	}
	if err := checkCount(recipe, role, scaleView(inst, recipe).Count-1); err != nil {
		return View{}, err
	}
	if err := m.store.Update(name, func(i *Instance) { i.Removing = []string{vm} }); err != nil {
		return View{}, err
	}
	if recipe.Console == consoleKubernetes {
		m.note(name, fmt.Sprintf("Removing %s from %s: moving its pods to the other nodes first.", vm, name))
	} else {
		m.note(name, fmt.Sprintf("Removing %s from %s.", vm, name))
	}
	m.startRemoval(name, vm)
	return m.Get(ctx, name)
}

func (m *Manager) startRemoval(name, vm string) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.remove(m.ctx, name, vm)
	}()
}

// remove does RemoveWorker's work. A drain that fails leaves the machine in
// place, schedulable again, and says why.
func (m *Manager) remove(ctx context.Context, name, vm string) {
	inst, err := m.store.Get(name)
	if err != nil {
		return // deleted meanwhile
	}
	recipe, _ := RecipeByID(inst.Recipe)
	role, _ := recipe.scalableRole()
	defer m.forgetCluster(name)

	if recipe.Console == consoleKubernetes {
		dctx, cancel := context.WithTimeout(ctx, drainTimeout)
		_, err := m.onControlPlane(dctx, inst, recipe, "sudo -n k3s kubectl drain "+shellQuote(vm)+
			" --ignore-daemonsets --delete-emptydir-data --force --timeout=150s 2>&1")
		cancel()
		if ctx.Err() != nil {
			return // shutting down; the removal resumes on the next start
		}
		if err != nil {
			m.onControlPlane(ctx, inst, recipe, "sudo -n k3s kubectl uncordon "+shellQuote(vm)) //nolint:errcheck // best effort: it was schedulable before
			if uerr := m.store.Update(name, func(i *Instance) { i.Removing = nil }); uerr != nil {
				log.Printf("services: %s: %v", name, uerr)
			}
			m.events.Append(events.Event{
				Level:   events.LevelError,
				Kind:    events.KindServiceProgress,
				VM:      name,
				Message: fmt.Sprintf("Could not move %s's pods away, so it stays in %s.", vm, name),
				Detail:  sentence(err.Error()),
			})
			return
		}
		if _, err := m.onControlPlane(ctx, inst, recipe, "sudo -n k3s kubectl delete node "+shellQuote(vm)+" --ignore-not-found"); err != nil {
			log.Printf("services: %s: delete node %s: %v", name, vm, err)
		}
	}

	if api, err := m.client(); err == nil {
		if vms, err := api.vms(ctx); err == nil {
			if _, exists := vms[vm]; exists {
				if err := api.deleteVM(ctx, vm); err != nil {
					log.Printf("services: %s: delete VM %s: %v", name, vm, err)
				}
			}
		}
	}
	if err := m.store.Update(name, func(i *Instance) {
		var kept []Member
		for _, mem := range i.members() {
			if mem.VM != vm {
				kept = append(kept, mem)
			}
		}
		i.Members = kept
		i.VMs = nil
		for _, mem := range kept {
			i.VMs = append(i.VMs, mem.VM)
		}
		delete(i.HostKeys, vm)
		unwatch(i, vm)
		if role.CountParam != "" {
			n := 0
			for _, mem := range kept {
				if mem.Role == role.ID {
					n++
				}
			}
			i.Params[role.CountParam] = strconv.Itoa(n)
		}
		i.Removing = nil
	}); err != nil {
		log.Printf("services: %s: record the removal of %s: %v", name, vm, err)
		return
	}
	m.note(name, fmt.Sprintf("Removed %s from %s.", vm, name))
}

// maxManifest bounds a manifest, which travels in one SSH command line that
// Linux limits to 128 KB.
const maxManifest = 64 << 10

// ApplyResult is what kubectl apply said.
type ApplyResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// Apply runs kubectl apply on a Kubernetes service's control plane with the
// given manifest. kubectl refusing it is a result, not an error.
func (m *Manager) Apply(ctx context.Context, name, manifest string) (ApplyResult, error) {
	inst, recipe, err := m.kubeService(name)
	if err != nil {
		return ApplyResult{}, err
	}
	switch {
	case strings.TrimSpace(manifest) == "":
		return ApplyResult{}, errorOf(ErrInvalid, "the manifest is empty")
	case len(manifest) > maxManifest:
		return ApplyResult{}, errorOf(ErrInvalid, "a manifest can be at most 64 KB; apply a larger one with kubectl")
	}
	actx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	enc := base64.StdEncoding.EncodeToString([]byte(manifest))
	out, err := m.onControlPlane(actx, inst, recipe, "echo "+enc+" | base64 -d | sudo -n k3s kubectl apply -f - 2>&1")
	if err != nil && strings.TrimSpace(out) == "" {
		var ke *kindError
		if errors.As(err, &ke) {
			return ApplyResult{}, err // the control plane is not up
		}
		out = err.Error()
	}
	res := ApplyResult{OK: err == nil, Output: strings.TrimSpace(out)}
	m.forgetCluster(name)
	if res.OK {
		m.events.Append(events.Event{Level: events.LevelInfo, Kind: events.KindServiceProgress, VM: name,
			Message: fmt.Sprintf("Applied a manifest to %s.", name), Detail: truncate(res.Output, 400)})
	}
	return res, nil
}

// Published is a NodePort a person published through the gateway.
type Published struct {
	NodePort int    `json:"node_port"`
	Label    string `json:"label"`
	Scheme   string `json:"scheme,omitempty"`
}

// publishedID is a published NodePort's endpoint ID.
func publishedID(nodePort int) string { return "np" + strconv.Itoa(nodePort) }

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9._/:-]{1,80}$`)

// maxPublished bounds how many NodePorts one service publishes.
const maxPublished = 20

// Publish gives one of a Kubernetes service's NodePorts a port on this host
// through the gateway, like the service's own endpoints.
func (m *Manager) Publish(ctx context.Context, name string, p Published) (View, error) {
	inst, _, err := m.kubeService(name)
	if err != nil {
		return View{}, err
	}
	switch {
	case p.NodePort < 30000 || p.NodePort > 32767:
		return View{}, errorOf(ErrInvalid, "a NodePort is between 30000 and 32767")
	case p.Scheme != "" && p.Scheme != "http" && p.Scheme != "https":
		return View{}, errorOf(ErrInvalid, "the scheme is http, https or none")
	case len(inst.Published) >= maxPublished:
		return View{}, errorOf(ErrInvalid, "%s publishes %d ports already; unpublish one first", name, maxPublished)
	}
	if !labelPattern.MatchString(p.Label) {
		p.Label = fmt.Sprintf("NodePort %d", p.NodePort)
	}
	for _, q := range inst.Published {
		if q.NodePort == p.NodePort {
			return View{}, errorOf(ErrExists, "%s publishes NodePort %d already", name, p.NodePort)
		}
	}

	m.portMu.Lock()
	list, err := m.store.List()
	if err == nil {
		e := Endpoint{ID: publishedID(p.NodePort), Port: p.NodePort}
		ports := m.allocatePorts([]Endpoint{e}, inst.Ports, takenPorts(list))
		err = m.store.Update(name, func(i *Instance) {
			i.Published = append(i.Published, p)
			i.Ports = ports
		})
	}
	m.portMu.Unlock()
	if err != nil {
		return View{}, err
	}
	m.syncGateway()
	m.note(name, fmt.Sprintf("Published %s's NodePort %d (%s).", name, p.NodePort, p.Label))
	return m.Get(ctx, name)
}

// Unpublish closes a published NodePort's port on this host.
func (m *Manager) Unpublish(ctx context.Context, name string, nodePort int) (View, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return View{}, err
	}
	found := false
	for _, p := range inst.Published {
		found = found || p.NodePort == nodePort
	}
	if !found {
		return View{}, errorOf(ErrNotFound, "%s does not publish NodePort %d", name, nodePort)
	}
	if err := m.store.Update(name, func(i *Instance) {
		var kept []Published
		for _, p := range i.Published {
			if p.NodePort != nodePort {
				kept = append(kept, p)
			}
		}
		i.Published = kept
		delete(i.Ports, publishedID(nodePort))
	}); err != nil {
		return View{}, err
	}
	m.syncGateway()
	return m.Get(ctx, name)
}
