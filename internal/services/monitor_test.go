package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// fakeProm stands in for a monitoring VM's Prometheus API.
type fakeProm struct {
	mu     sync.Mutex
	alerts []map[string]any
	down   bool
	calls  int
}

func (p *fakeProm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.down {
		http.Error(w, "connection refused", http.StatusBadGateway)
		return
	}
	var data any
	switch r.URL.Path {
	case "/api/v1/alerts":
		data = map[string]any{"alerts": p.alerts}
	case "/api/v1/targets":
		target := func(job, service, instance, recipe, health string) map[string]any {
			return map[string]any{"labels": map[string]string{"job": job, "service": service, "instance": instance, "recipe": recipe}, "health": health}
		}
		data = map[string]any{"activeTargets": []any{
			target("prometheus", "obs", "obs", "", "up"),
			target("node", "obs", "obs", "observability", "up"),
			target("node", "db", "db", "postgres", "up"),
			target("postgres", "db", "db", "postgres", "down"),
		}}
	case "/api/v1/rules":
		rule := func(name, title, state string, alerts ...string) map[string]any {
			var as []any
			for _, s := range alerts {
				as = append(as, map[string]any{"state": s})
			}
			return map[string]any{"name": name, "type": "alerting", "state": state, "annotations": map[string]string{"title": title, "when": "now and then"}, "alerts": as}
		}
		data = map[string]any{"groups": []any{map[string]any{"rules": []any{
			rule("VMDown", "VM down", "inactive"),
			rule("ServiceUnreachable", "Service unreachable", "firing", "firing"),
			rule("DiskFillingUp", "Disk filling up", "pending", "pending"),
		}}}}
	case "/api/v1/query":
		data = map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{1.7e9, "2254857830"}}}}
	case "/api/v1/query_range":
		data = map[string]any{"resultType": "matrix", "result": []any{
			map[string]any{"metric": map[string]string{"service": "db"}, "values": []any{[]any{1.7e9, "12.345"}, []any{1.7e9 + 60, "NaN"}, []any{1.7e9 + 120, "15"}}},
			map[string]any{"metric": map[string]string{"service": "a-cluster"}, "values": []any{[]any{1.7e9, "40"}}},
		}}
	case "/api/v1/status/runtimeinfo":
		data = map[string]any{"storageRetention": "15d"}
	default:
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data}) //nolint:errcheck
}

func (p *fakeProm) set(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn()
}

// watchedHarness has a ready monitoring service "obs" (at 10.0.0.5) and a
// ready PostgreSQL "db" (at 10.0.0.6), and a Prometheus that answers.
func watchedHarness(t *testing.T) (*harness, *fakeProm) {
	t.Helper()
	h := newHarness(t)
	h.guest.set("ready")
	h.m.vmCacheTTL = 0
	prom := &fakeProm{}
	srv := httptest.NewServer(prom)
	t.Cleanup(srv.Close)
	h.m.promURL = func(string) string { return srv.URL }
	for _, req := range []CreateRequest{{Name: "obs", Recipe: "observability"}, {Name: "db", Recipe: "postgres"}} {
		if _, err := h.m.Create(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		h.waitState(t, req.Name, StateReady)
	}
	return h, prom
}

// settle runs looks until the setups they start are done.
func (h *harness) settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.m.monitorOnce(context.Background())
		h.m.mon.mu.Lock()
		running := h.m.mon.running
		h.m.mon.mu.Unlock()
		if !running {
			h.m.monitorOnce(context.Background())
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the exporter setups never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMonitor_SetsEveryServiceUpAndTellsPrometheus(t *testing.T) {
	h, _ := watchedHarness(t)
	h.settle(t)

	db, _ := h.store.Get("db")
	if want := []string{"db/node", "db/postgres"}; !slices.Equal(db.Watched, want) {
		t.Errorf("db watched = %v, want %v", db.Watched, want)
	}
	obs, _ := h.store.Get("obs")
	if !slices.Equal(obs.Watched, []string{"obs/node"}) || obs.Provisioned == "" {
		t.Errorf("obs watched = %v, provisioned %q", obs.Watched, obs.Provisioned)
	}

	// The targets went to the monitoring VM, labelled with service and VM.
	var groups []targetGroup
	if err := json.Unmarshal(h.guest.input("10.0.0.5", "targets.tmp"), &groups); err != nil {
		t.Fatalf("targets sent: %v", err)
	}
	var got []string
	for _, g := range groups {
		got = append(got, g.Labels["service"]+" "+g.Labels["instance"]+" "+g.Labels["job"]+" "+g.Targets[0])
	}
	want := []string{"db db node 10.0.0.6:9100", "db db postgres 10.0.0.6:9187", "obs obs node 10.0.0.5:9100"}
	if !slices.Equal(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}

	// The configuration, rules and dashboards went there too, once.
	files := untar(t, h.guest.input("10.0.0.5", "nano-cloud monitoring setup"))
	for _, f := range []string{"etc/prometheus/prometheus.yml", "etc/prometheus/nanocloud-rules.yml", "etc/grafana/provisioning/dashboards/nanocloud.yaml", "var/lib/grafana/dashboards/nanocloud/overview.json"} {
		if files[f] == "" {
			t.Errorf("the monitoring setup lacks %s; has %v", f, keys(files))
		}
	}
	if !strings.Contains(files["etc/prometheus/prometheus.yml"], `service: "obs", instance: "obs"`) {
		t.Errorf("prometheus.yml does not label Prometheus itself:\n%s", files["etc/prometheus/prometheus.yml"])
	}
	before := h.countCmds("nano-cloud monitoring setup") + h.countCmds("nano-cloud metrics: ") + h.countCmds("targets.tmp")
	h.m.monitorOnce(context.Background())
	if after := h.countCmds("nano-cloud monitoring setup") + h.countCmds("nano-cloud metrics: ") + h.countCmds("targets.tmp"); after != before {
		t.Errorf("a look with nothing new ran %d more commands", after-before)
	}

	evs, _ := h.events.Since(0, 0)
	if !hasEvent(evs, "obs now watches every service") {
		t.Errorf("no word that monitoring is set up: %+v", evs)
	}
}

func TestMonitor_DoesNothingWithoutAMonitoringService(t *testing.T) {
	h := newHarness(t)
	h.guest.set("ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateReady)
	h.m.monitorOnce(context.Background())
	if n := h.countCmds("nano-cloud metrics: "); n != 0 {
		t.Errorf("set up %d exporters with nothing to read them", n)
	}
}

func TestMonitor_TriesAFailedSetupAgainLater(t *testing.T) {
	h, _ := watchedHarness(t)
	h.guest.mu.Lock()
	h.guest.setupFail = map[string]bool{"postgres": true}
	h.guest.mu.Unlock()
	h.settle(t)

	db, _ := h.store.Get("db")
	if !slices.Equal(db.Watched, []string{"db/node"}) {
		t.Errorf("db watched = %v, want the node exporter only", db.Watched)
	}
	v, err := h.m.Monitoring(context.Background(), "obs")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Setup) != 1 || v.Setup[0].VM != "db" || !strings.Contains(v.Setup[0].Problem, "Unable to locate package") {
		t.Errorf("setup = %+v, want db's PostgreSQL metrics with the failure", v.Setup)
	}

	// Not again right away...
	h.settle(t)
	if n := h.countCmds("nano-cloud metrics: postgres"); n != 1 {
		t.Errorf("tried the failed setup %d times, want once until the retry is due", n)
	}
	// ...but once the retry is due, and this time it works.
	h.guest.mu.Lock()
	h.guest.setupFail = nil
	h.guest.mu.Unlock()
	h.m.setupRetry = 0
	h.settle(t)
	db, _ = h.store.Get("db")
	if !slices.Contains(db.Watched, "db/postgres") {
		t.Errorf("db watched = %v after the retry", db.Watched)
	}
}

func TestMonitor_ShowsAlertsInTheFeedAndOnTheService(t *testing.T) {
	h, prom := watchedHarness(t)
	h.settle(t)
	disk := map[string]any{
		"labels":      map[string]string{"alertname": "DiskFillingUp", "service": "db", "instance": "db", "severity": "warning"},
		"annotations": map[string]string{"title": "Disk filling up", "summary": "db's disk is 91% full"},
		"state":       "firing",
		"activeAt":    "2026-10-26T20:00:00Z",
	}
	pending := map[string]any{
		"labels": map[string]string{"alertname": "VMDown", "service": "db", "instance": "db"},
		"state":  "pending",
	}
	prom.set(func() { prom.alerts = []map[string]any{disk, pending} })
	h.m.monitorOnce(context.Background())

	v, _ := h.m.Get(context.Background(), "db")
	if len(v.Alerts) != 1 || v.Alerts[0].Title != "Disk filling up" || v.Alerts[0].SinceUnix == 0 {
		t.Fatalf("db alerts = %+v, want the firing disk alert only", v.Alerts)
	}
	evs, _ := h.events.Since(0, 0)
	fired := lastEvent(evs, events.KindServiceAlert)
	if fired.VM != "db" || fired.Level != events.LevelWarn || fired.Message != "Disk filling up: db's disk is 91% full." {
		t.Errorf("alert event = %+v", fired)
	}

	// Prometheus not answering is not the alert ending.
	prom.set(func() { prom.down = true })
	h.m.monitorOnce(context.Background())
	if v, _ := h.m.Get(context.Background(), "db"); len(v.Alerts) != 1 {
		t.Errorf("alerts = %+v while Prometheus was away, want the one still firing", v.Alerts)
	}

	prom.set(func() { prom.down, prom.alerts = false, nil })
	h.m.monitorOnce(context.Background())
	if v, _ := h.m.Get(context.Background(), "db"); len(v.Alerts) != 0 {
		t.Errorf("alerts = %+v after it ended", v.Alerts)
	}
	evs, _ = h.events.Since(0, 0)
	if e := lastEvent(evs, events.KindServiceAlert); e.Level != events.LevelInfo || !strings.HasPrefix(e.Message, "Resolved: Disk filling up") {
		t.Errorf("resolved event = %+v", e)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == events.KindServiceAlert {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d alert events, want one when it fired and one when it ended", n)
	}
}

func TestMonitor_StopsWatchingAStoppedService(t *testing.T) {
	h, _ := watchedHarness(t)
	h.settle(t)
	if err := h.m.Stop(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	h.m.monitorOnce(context.Background())
	var groups []targetGroup
	if err := json.Unmarshal(h.guest.input("10.0.0.5", "targets.tmp"), &groups); err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Labels["service"] == "db" {
			t.Errorf("still a target once stopped, so its VM-down alert would fire: %+v", g)
		}
	}
}

func TestMonitoring_ReadsPrometheus(t *testing.T) {
	h, prom := watchedHarness(t)
	h.settle(t)
	v, err := h.m.Monitoring(context.Background(), "obs")
	if err != nil {
		t.Fatal(err)
	}
	if v.TargetsUp != 3 || v.TargetsTotal != 4 || v.VMs != 2 || v.Services != 2 {
		t.Errorf("targets %d/%d, %d VMs in %d services", v.TargetsUp, v.TargetsTotal, v.VMs, v.Services)
	}
	if len(v.Watching) != 2 || v.Watching[0].Service != "db" || v.Watching[0].Recipe != "postgres" ||
		!slices.Equal(v.Watching[0].Jobs, []string{"node", "postgres"}) || v.Watching[0].Up != 1 || v.Watching[0].Total != 2 {
		t.Errorf("watching = %+v", v.Watching)
	}
	if !slices.Equal(v.Watching[1].Jobs, []string{"node", "prometheus"}) {
		t.Errorf("obs jobs = %v, want the node exporter first", v.Watching[1].Jobs)
	}
	var states []string
	for _, r := range v.Rules {
		states = append(states, r.Name+"="+r.State)
	}
	if !slices.Equal(states, []string{"VM down=ok", "Service unreachable=firing", "Disk filling up=pending"}) || v.Firing != 1 {
		t.Errorf("rules = %v, %d firing", states, v.Firing)
	}
	if v.StoredBytes != 2254857830 || v.Retention != "15d" {
		t.Errorf("stored %d for %q", v.StoredBytes, v.Retention)
	}
	if len(v.CPU) != 2 || v.CPU[0].Name != "a-cluster" || len(v.CPU[1].Points) != 2 || v.CPU[1].Points[0][1] != 12.3 {
		t.Errorf("cpu = %+v, want two series by name, NaN skipped, rounded", v.CPU)
	}
	if !v.Provisioned || len(v.Dashboards) != len(dashboards()) || !v.Dashboards[0].Home {
		t.Errorf("provisioned %v, dashboards %+v", v.Provisioned, v.Dashboards)
	}

	// Read again within moments: from what was read.
	calls := prom.calls
	if _, err := h.m.Monitoring(context.Background(), "obs"); err != nil {
		t.Fatal(err)
	}
	if prom.calls != calls {
		t.Errorf("read Prometheus again %d times right away", prom.calls-calls)
	}

	if _, err := h.m.Monitoring(context.Background(), "db"); err == nil || !strings.Contains(err.Error(), "not a monitoring service") {
		t.Errorf("monitoring of a database: %v", err)
	}
}

func TestMonitor_ProvisioningFailureIsTriedAgain(t *testing.T) {
	h, _ := watchedHarness(t)
	h.guest.mu.Lock()
	h.guest.provisionFail = true
	h.guest.mu.Unlock()
	h.settle(t)
	if obs, _ := h.store.Get("obs"); obs.Provisioned != "" {
		t.Error("recorded a monitoring setup that failed")
	}
	if n := h.countCmds("targets.tmp"); n != 0 {
		t.Errorf("sent targets %d times to a Prometheus not set up", n)
	}
	h.guest.mu.Lock()
	h.guest.provisionFail = false
	h.guest.mu.Unlock()
	h.m.setupRetry = 0
	h.settle(t)
	if obs, _ := h.store.Get("obs"); obs.Provisioned == "" {
		t.Error("never set monitoring up after the failure")
	}
}

func TestMonitoringBundle_IsStableAndPerService(t *testing.T) {
	a, sumA, err := monitoringBundle("obs", "obs")
	if err != nil {
		t.Fatal(err)
	}
	_, again, _ := monitoringBundle("obs", "obs")
	_, other, _ := monitoringBundle("obs-2", "obs-2")
	if sumA != again || sumA == other {
		t.Errorf("hashes %s, %s, %s: want the same bundle to hash the same and another service's not", sumA, again, other)
	}
	files := untar(t, a)
	n := 0
	for name := range files {
		if strings.HasPrefix(name, "var/lib/grafana/dashboards/nanocloud/") {
			n++
		}
	}
	if n != len(dashboards()) {
		t.Errorf("%d dashboards in the bundle, want %d", n, len(dashboards()))
	}
}

func TestRules_EachSaysWhatAndWhen(t *testing.T) {
	var doc struct {
		Groups []struct {
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expr        string            `yaml:"expr"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal([]byte(monitoringFile("rules.yml")), &doc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			if r.Alert == "" || seen[r.Alert] || r.Expr == "" || !balanced(r.Expr) {
				t.Errorf("rule %q: needs a name of its own and a balanced expression: %q", r.Alert, r.Expr)
			}
			seen[r.Alert] = true
			for _, a := range []string{"title", "when", "summary"} {
				if r.Annotations[a] == "" {
					t.Errorf("rule %s has no %s", r.Alert, a)
				}
			}
			if s := r.Labels["severity"]; s != "warning" && s != "critical" {
				t.Errorf("rule %s severity %q", r.Alert, s)
			}
		}
	}
	if len(seen) != 4 {
		t.Errorf("%d rules, want 4", len(seen))
	}
}

func TestExporterScripts_AreThere(t *testing.T) {
	for id, e := range exporters {
		script := monitoringFile(e.script)
		if !strings.HasPrefix(script, "# nano-cloud metrics: "+e.Job+"\n") {
			t.Errorf("%s's script does not start by naming its job %q", id, e.Job)
		}
		if !strings.Contains(script, "answers ") {
			t.Errorf("%s's script does not check its exporter answers", id)
		}
	}
	if !strings.HasPrefix(setupCmd(exporters["postgres"]), "sudo -n bash -c 'set -e") {
		t.Errorf("setup command = %.60q", setupCmd(exporters["postgres"]))
	}
}

func TestExportersOn_ClusterGetsOneKubeStateMetrics(t *testing.T) {
	recipe, _ := RecipeByID("kubernetes_cluster")
	inst := Instance{Members: []Member{{Role: "server", VM: "lab-server"}, {Role: "agent", VM: "lab-agent-1"}}}
	jobs := func(mem Member) (out []string) {
		for _, e := range exportersOn(recipe, inst, mem) {
			out = append(out, e.Job)
		}
		return out
	}
	if got := jobs(inst.Members[0]); !slices.Equal(got, []string{"node", "kube-state-metrics"}) {
		t.Errorf("control plane gets %v", got)
	}
	if got := jobs(inst.Members[1]); !slices.Equal(got, []string{"node"}) {
		t.Errorf("worker gets %v", got)
	}
}

func TestUnwatch_ForgetsOneVM(t *testing.T) {
	inst := Instance{Watched: []string{"lab-agent-1/node", "lab-agent-10/node", "lab-server/node", "lab-server/kube-state-metrics"}}
	unwatch(&inst, "lab-agent-1")
	unwatch(&inst, "lab-server")
	if !slices.Equal(inst.Watched, []string{"lab-agent-10/node"}) {
		t.Errorf("watched = %v", inst.Watched)
	}
}

func TestCatalog_RejectsUnknownMetrics(t *testing.T) {
	r, _ := RecipeByID("postgres")
	r.Metrics = []Metric{{Exporter: "redis"}}
	if err := r.validate(); err == nil || !strings.Contains(err.Error(), "redis") {
		t.Errorf("unknown exporter: %v", err)
	}
	r.Metrics = []Metric{{Exporter: "postgres", Role: "replica"}}
	if err := r.validate(); err == nil || !strings.Contains(err.Error(), "replica") {
		t.Errorf("unknown role: %v", err)
	}
}

// balanced says brackets and quotes pair up in a query.
func balanced(expr string) bool {
	var stack []rune
	pairs := map[rune]rune{')': '(', '}': '{', ']': '['}
	inQuote := false
	for _, c := range expr {
		switch {
		case c == '"':
			inQuote = !inQuote
		case inQuote:
		case c == '(' || c == '{' || c == '[':
			stack = append(stack, c)
		case pairs[c] != 0:
			if len(stack) == 0 || stack[len(stack)-1] != pairs[c] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return !inQuote && len(stack) == 0
}

func untar(t *testing.T, gz []byte) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	tr := tar.NewReader(zr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[hdr.Name] = string(b)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func hasEvent(evs []events.Event, sub string) bool {
	for _, e := range evs {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

func lastEvent(evs []events.Event, kind string) events.Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == kind {
			return evs[i]
		}
	}
	return events.Event{}
}
