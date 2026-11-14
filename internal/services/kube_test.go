package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// clusterFixture is kubectl get nodes,deployments,services,pods -A -o json
// for a control plane and a worker, then kubectl top nodes.
const clusterFixture = `{"apiVersion": "v1", "kind": "List", "items": [
 {"kind": "Node", "metadata": {"name": "lab-server", "labels": {"node-role.kubernetes.io/control-plane": "true", "node-role.kubernetes.io/master": "true"}},
  "status": {"addresses": [{"type": "InternalIP", "address": "10.0.0.5"}, {"type": "Hostname", "address": "lab-server"}],
             "conditions": [{"type": "MemoryPressure", "status": "False"}, {"type": "Ready", "status": "True"}],
             "nodeInfo": {"kubeletVersion": "v1.36.5+k3s1"}}},
 {"kind": "Node", "metadata": {"name": "lab-agent-1", "labels": {}}, "spec": {"unschedulable": true},
  "status": {"addresses": [{"type": "InternalIP", "address": "10.0.0.6"}],
             "conditions": [{"type": "Ready", "status": "False"}], "nodeInfo": {"kubeletVersion": "v1.36.5+k3s1"}}},
 {"kind": "Deployment", "metadata": {"name": "web", "namespace": "default", "creationTimestamp": "2026-10-26T08:00:00Z"},
  "spec": {"replicas": 3, "template": {"spec": {"containers": [{"image": "nginx:1.27"}, {"image": "busybox"}]}}}, "status": {"readyReplicas": 2}},
 {"kind": "Service", "metadata": {"name": "kubernetes", "namespace": "default"}, "spec": {"type": "ClusterIP", "ports": [{"port": 443, "protocol": "TCP"}]}},
 {"kind": "Service", "metadata": {"name": "web", "namespace": "default"}, "spec": {"type": "NodePort", "ports": [{"name": "http", "port": 80, "nodePort": 30080, "protocol": "TCP"}]}},
 {"kind": "Pod", "metadata": {"name": "web-1", "namespace": "default"}, "spec": {"nodeName": "lab-agent-1"}, "status": {"phase": "Running"}},
 {"kind": "Pod", "metadata": {"name": "web-2", "namespace": "default"}, "spec": {"nodeName": "lab-server"}, "status": {"phase": "Running"}},
 {"kind": "Pod", "metadata": {"name": "web-3", "namespace": "default"}, "spec": {}, "status": {"phase": "Pending"}},
 {"kind": "Pod", "metadata": {"name": "job-1", "namespace": "default"}, "spec": {"nodeName": "lab-server"}, "status": {"phase": "Succeeded"}}
]}
` + topMarker + `
lab-server    183m   9%    1612Mi   41%
lab-agent-1   <unknown>
`

func TestParseCluster(t *testing.T) {
	c, err := parseCluster(clusterFixture)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "v1.36.5+k3s1" || len(c.Nodes) != 2 {
		t.Fatalf("cluster = %+v", c)
	}
	cp, w := c.Nodes[0], c.Nodes[1]
	if !cp.Ready || strings.Join(cp.Roles, ",") != "control-plane" || cp.IP != "10.0.0.5" || cp.Pods != 1 {
		t.Errorf("control plane = %+v", cp)
	}
	if cp.CPUPercent == nil || *cp.CPUPercent != 9 || cp.MemoryPercent == nil || *cp.MemoryPercent != 41 {
		t.Errorf("control plane usage = %v / %v", cp.CPUPercent, cp.MemoryPercent)
	}
	if w.Ready || !w.Unschedulable || len(w.Roles) != 0 || w.Pods != 1 || w.CPUPercent != nil {
		t.Errorf("worker = %+v", w)
	}
	if len(c.Deployments) != 1 {
		t.Fatalf("deployments = %+v", c.Deployments)
	}
	d := c.Deployments[0]
	if d.Name != "web" || d.Ready != 2 || d.Desired != 3 || strings.Join(d.Images, " ") != "nginx:1.27 busybox" || d.CreatedUnix == 0 {
		t.Errorf("deployment = %+v", d)
	}
	if len(c.Exposed) != 1 || c.Exposed[0].Name != "web" || c.Exposed[0].Ports[0].NodePort != 30080 {
		t.Errorf("exposed = %+v; want only the NodePort service", c.Exposed)
	}
	if c.Pods != (PodCounts{Total: 4, Running: 2, Pending: 1, Succeeded: 1}) {
		t.Errorf("pods = %+v", c.Pods)
	}
	if _, err := parseCluster("error: You must be logged in"); err == nil {
		t.Error("parsed something that is not kubectl's JSON")
	}
}

// readyCluster is a ready Kubernetes cluster with the given number of workers.
func readyCluster(t *testing.T, workers string) *harness {
	t.Helper()
	h := newHarness(t)
	h.guest.set("ready")
	h.guest.cluster = clusterFixture
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": workers}}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "lab", StateReady)
	return h
}

func (h *harness) countCmds(sub string) int {
	h.guest.mu.Lock()
	defer h.guest.mu.Unlock()
	n := 0
	for _, c := range h.guest.cmds {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func TestCluster_ReadsTheClusterOnceInAWhile(t *testing.T) {
	h := readyCluster(t, "1")
	c, err := h.m.Cluster(context.Background(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Nodes) != 2 || c.ReadAtUnix == 0 {
		t.Errorf("cluster = %+v", c)
	}
	if _, err := h.m.Cluster(context.Background(), "lab"); err != nil {
		t.Fatal(err)
	}
	if n := h.countCmds("kubectl get nodes,deployments"); n != 1 {
		t.Errorf("read the cluster %d times in a row, want once", n)
	}

	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Cluster(context.Background(), "db"); !errors.Is(err, ErrInvalid) {
		t.Errorf("cluster of a database: %v, want ErrInvalid", err)
	}
}

func TestAddWorkers_BuildsTheNewMachinesWhileTheClusterServes(t *testing.T) {
	h := readyCluster(t, "1")
	v, err := h.m.AddWorkers(context.Background(), "lab", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateCreating || strings.Join(v.Adding, ",") != "lab-agent-2" || v.Scale.Count != 2 {
		t.Errorf("after adding: %s, adding %v, scale %+v", v.State, v.Adding, v.Scale)
	}
	inst, _ := h.store.Get("lab")
	if inst.Params["workers"] != "2" || strings.Join(inst.VMs, ",") != "lab-server,lab-agent,lab-agent-2" {
		t.Errorf("stored: workers %s, VMs %v", inst.Params["workers"], inst.VMs)
	}
	v = h.waitState(t, "lab", StateReady)
	if len(v.Adding) != 0 {
		t.Errorf("still adding %v once ready", v.Adding)
	}
	h.api.with(func() {
		if len(h.api.created) != 3 || h.api.created[2].Name != "lab-agent-2" {
			t.Errorf("created %+v", h.api.created)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _ := h.events.Since(0, 0)
		if last := evs[len(evs)-1]; last.Message == "lab-agent-2 joined lab." {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no joined note in %+v", evs)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAddWorkers_KeepsToTheRecipesLimits(t *testing.T) {
	h := readyCluster(t, "1")
	if _, err := h.m.AddWorkers(context.Background(), "lab", 5); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "from 1 to 5 workers, not 6") {
		t.Errorf("a sixth worker: %v", err)
	}
	if _, err := h.m.AddWorkers(context.Background(), "lab", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.AddWorkers(context.Background(), "lab", 1); !errors.Is(err, ErrNotReady) {
		t.Errorf("adding while it builds: %v, want ErrNotReady", err)
	}
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.AddWorkers(context.Background(), "db", 1); !errors.Is(err, ErrInvalid) {
		t.Errorf("a worker for a database: %v, want ErrInvalid", err)
	}
}

func TestRemoveWorker_DrainsThenDeletes(t *testing.T) {
	h := readyCluster(t, "2")
	v, err := h.m.RemoveWorker(context.Background(), "lab", "lab-agent-2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v.Removing, ",") != "lab-agent-2" {
		t.Errorf("removing = %v", v.Removing)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inst, _ := h.store.Get("lab")
		if len(inst.Removing) == 0 {
			if strings.Join(inst.VMs, ",") != "lab-server,lab-agent-1" || inst.Params["workers"] != "1" {
				t.Errorf("after removal: VMs %v, workers %s", inst.VMs, inst.Params["workers"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the removal never finished")
		}
		time.Sleep(time.Millisecond)
	}
	if h.countCmds("kubectl drain 'lab-agent-2'") != 1 || h.countCmds("kubectl delete node 'lab-agent-2'") != 1 {
		t.Error("the node was not drained and deleted from the cluster")
	}
	h.api.with(func() {
		if len(h.api.deleted) != 1 || h.api.deleted[0] != "lab-agent-2" {
			t.Errorf("deleted VMs %v", h.api.deleted)
		}
	})
}

// A drain that cannot finish leaves the worker where it was.
func TestRemoveWorker_KeepsAWorkerItCannotDrain(t *testing.T) {
	h := readyCluster(t, "2")
	h.guest.drainFail = true
	if _, err := h.m.RemoveWorker(context.Background(), "lab", "lab-agent-2"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inst, _ := h.store.Get("lab")
		if len(inst.Removing) == 0 {
			if len(inst.VMs) != 3 {
				t.Errorf("VMs after a failed drain: %v", inst.VMs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the removal never ended")
		}
		time.Sleep(time.Millisecond)
	}
	if h.countCmds("kubectl uncordon 'lab-agent-2'") != 1 {
		t.Error("the worker was left cordoned")
	}
	h.api.with(func() {
		if len(h.api.deleted) != 0 {
			t.Errorf("deleted %v after a failed drain", h.api.deleted)
		}
	})
	evs, _ := h.events.Since(0, 0)
	if last := evs[len(evs)-1]; last.Level != "error" || !strings.Contains(last.Detail, "disruption budget") {
		t.Errorf("last event = %+v", last)
	}
}

func TestRemoveWorker_RefusesWhatItShouldNot(t *testing.T) {
	h := readyCluster(t, "1")
	if _, err := h.m.RemoveWorker(context.Background(), "lab", "lab-agent"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not 0") {
		t.Errorf("the last worker: %v", err)
	}
	if _, err := h.m.RemoveWorker(context.Background(), "lab", "lab-server"); !errors.Is(err, ErrInvalid) {
		t.Errorf("the control plane: %v", err)
	}
}

func TestApply_RunsKubectlApplyAndReportsWhatItSaid(t *testing.T) {
	h := readyCluster(t, "1")
	res, err := h.m.Apply(context.Background(), "lab", "apiVersion: apps/v1\nkind: Deployment\n")
	if err != nil || !res.OK || res.Output != "deployment.apps/hello created" {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	h.guest.applyFail = true
	res, err = h.m.Apply(context.Background(), "lab", "kind: : bad")
	if err != nil || res.OK || !strings.Contains(res.Output, "mapping values are not allowed") {
		t.Errorf("a bad manifest: %+v, %v; want kubectl's complaint", res, err)
	}
	for name, manifest := range map[string]string{"empty": "  \n", "too big": strings.Repeat("a", maxManifest+1)} {
		if _, err := h.m.Apply(context.Background(), "lab", manifest); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s manifest: %v, want ErrInvalid", name, err)
		}
	}
}

func TestPublish_ForwardsANodePortThroughTheGateway(t *testing.T) {
	h := readyCluster(t, "1")
	h.m.vmCacheTTL = 0
	ctx := context.Background()
	v, err := h.m.Publish(ctx, "lab", Published{NodePort: 30080, Label: "default/web", Scheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	var ev EndpointView
	for _, e := range v.Endpoints {
		if e.ID == "np30080" {
			ev = e
		}
	}
	if !ev.Published || ev.HostPort != 30080 || ev.Address != "localhost:30080" || ev.Label != "default/web" || ev.Scheme != "http" {
		t.Errorf("published endpoint = %+v", ev)
	}
	waitListening(t, h, "lab", "np30080")
	if target, rerr := h.m.resolveEndpoint(ctx, "lab", "np30080"); rerr != nil || target != "10.0.0.5:30080" {
		t.Errorf("forwards to %q, %v; want a node at the NodePort", target, rerr)
	}

	if _, err = h.m.Publish(ctx, "lab", Published{NodePort: 30080}); !errors.Is(err, ErrExists) {
		t.Errorf("publish twice: %v", err)
	}
	if _, err = h.m.Publish(ctx, "lab", Published{NodePort: 8080}); !errors.Is(err, ErrInvalid) {
		t.Errorf("not a NodePort: %v", err)
	}

	v, err = h.m.Unpublish(ctx, "lab", 30080)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range v.Endpoints {
		if e.ID == "np30080" {
			t.Error("still published after unpublishing")
		}
	}
	if ok, _ := h.m.gw.status("lab", "np30080"); ok {
		t.Error("the gateway still listens for an unpublished NodePort")
	}
	if _, err := h.m.Unpublish(ctx, "lab", 30080); !errors.Is(err, ErrNotFound) {
		t.Errorf("unpublish twice: %v", err)
	}
}

func TestNextNames_NumbersOnFromTheHighest(t *testing.T) {
	recipe, _ := RecipeByID("kubernetes_cluster")
	role, _ := recipe.scalableRole()
	for _, tc := range []struct {
		vms  []string
		want string
	}{
		{[]string{"lab-server", "lab-agent"}, "lab-agent-2,lab-agent-3"},
		{[]string{"lab-server", "lab-agent-1", "lab-agent-4"}, "lab-agent-5,lab-agent-6"},
	} {
		inst := Instance{Name: "lab"}
		for i, vm := range tc.vms {
			r := "agent"
			if i == 0 {
				r = "server"
			}
			inst.Members = append(inst.Members, Member{Role: r, VM: vm})
		}
		if got := strings.Join(nextNames(inst, role, 2), ","); got != tc.want {
			t.Errorf("after %v: %s, want %s", tc.vms, got, tc.want)
		}
	}
}
