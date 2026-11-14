package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// promClient reads a monitoring service's Prometheus over its HTTP API.
type promClient struct {
	base string
	hc   *http.Client
}

// prom returns a client for the Prometheus at ip.
func (m *Manager) prom(ip string) promClient {
	return promClient{base: m.promURL(ip), hc: m.httpc}
}

// get calls an API path and decodes its data into out.
func (p promClient) get(ctx context.Context, path string, q url.Values, out any) error {
	u := p.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var env struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s answered %s", path, resp.Status)
	}
	if env.Status != "success" {
		return errors.New(env.Error)
	}
	return json.Unmarshal(env.Data, out)
}

// alerts returns the alerts firing.
func (p promClient) alerts(ctx context.Context) ([]Alert, error) {
	var data struct {
		Alerts []struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			State       string            `json:"state"`
			ActiveAt    time.Time         `json:"activeAt"`
		} `json:"alerts"`
	}
	if err := p.get(ctx, "/api/v1/alerts", nil, &data); err != nil {
		return nil, err
	}
	var out []Alert
	for _, a := range data.Alerts {
		if a.State != "firing" {
			continue
		}
		title := a.Annotations["title"]
		if title == "" {
			title = a.Labels["alertname"]
		}
		al := Alert{
			Name:     a.Labels["alertname"],
			Title:    title,
			Summary:  a.Annotations["summary"],
			Severity: a.Labels["severity"],
			Service:  a.Labels["service"],
			VM:       a.Labels["instance"],
			key:      labelKey(a.Labels),
		}
		if !a.ActiveAt.IsZero() {
			al.SinceUnix = a.ActiveAt.Unix()
		}
		out = append(out, al)
	}
	return out, nil
}

// labelKey is a label set as one string, the same for the same labels.
func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + labels[k] + "\x00")
	}
	return b.String()
}

// MonitoringView is what a monitoring service's console shows.
type MonitoringView struct {
	// TargetsUp of TargetsTotal answered their last scrape.
	TargetsUp    int `json:"targets_up"`
	TargetsTotal int `json:"targets_total"`
	// VMs and Services are how many it watches, Watching each service.
	VMs      int         `json:"vms"`
	Services int         `json:"services"`
	Watching []WatchView `json:"watching"`
	// Rules are its alert rules, Firing how many alerts fire.
	Rules  []RuleView `json:"rules"`
	Firing int        `json:"firing"`
	// StoredBytes is the on-disk size of its data, kept for Retention.
	StoredBytes int64  `json:"stored_bytes"`
	Retention   string `json:"retention,omitempty"`
	// CPU is each service's CPU use over the last hour.
	CPU        []SeriesView    `json:"cpu"`
	Dashboards []DashboardView `json:"dashboards"`
	// Provisioned says nano-cloud's dashboards and rules are installed;
	// Setup are exporters on their way, or that failed to set up.
	Provisioned bool        `json:"provisioned"`
	Setup       []SetupView `json:"setup,omitempty"`
	ReadAtUnix  int64       `json:"read_at_unix"`
}

// WatchView is one watched service.
type WatchView struct {
	Service string   `json:"service"`
	Recipe  string   `json:"recipe,omitempty"`
	VMs     int      `json:"vms"`
	Jobs    []string `json:"jobs"`
	Up      int      `json:"up"`
	Total   int      `json:"total"`
}

// RuleView is an alert rule and its state: ok, pending or firing.
type RuleView struct {
	Name  string `json:"name"`
	When  string `json:"when,omitempty"`
	State string `json:"state"`
}

// SeriesView is one line of a chart: [unix seconds, value] points.
type SeriesView struct {
	Name   string       `json:"name"`
	Points [][2]float64 `json:"points"`
}

// DashboardView is a Grafana dashboard nano-cloud provides. A dashboard for
// one kind of service names its recipes and the variable selecting the service.
type DashboardView struct {
	UID     string   `json:"uid"`
	Title   string   `json:"title"`
	About   string   `json:"about"`
	Home    bool     `json:"home,omitempty"`
	Var     string   `json:"var,omitempty"`
	Recipes []string `json:"recipes,omitempty"`
}

// SetupView is an exporter being set up on a VM, waiting to be, or failed.
type SetupView struct {
	Service string `json:"service"`
	VM      string `json:"vm"`
	What    string `json:"what"`
	Running bool   `json:"running,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// monitoringTTL is how long a Prometheus reading is reused.
const monitoringTTL = 10 * time.Second

// cpuQuery is each service's CPU use, in percent of its VMs' CPUs.
const cpuQuery = `100 * (1 - avg by (service) (rate(node_cpu_seconds_total{mode="idle"}[2m])))`

// storedQuery is Prometheus's on-disk size; each part may be missing,
// depending on the version.
const storedQuery = `(sum(prometheus_tsdb_storage_blocks_bytes) or vector(0))` +
	` + (sum(prometheus_tsdb_head_chunks_storage_size_bytes) or vector(0))` +
	` + (sum(prometheus_tsdb_wal_storage_size_bytes) or vector(0))`

// Monitoring reads what a monitoring service watches from its Prometheus.
func (m *Manager) Monitoring(ctx context.Context, name string) (MonitoringView, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return MonitoringView{}, err
	}
	recipe, _ := RecipeByID(inst.Recipe)
	if recipe.Console != consoleMonitoring {
		return MonitoringView{}, errorOf(ErrInvalid, "%s is not a monitoring service", name)
	}
	if inst.State != StateReady {
		return MonitoringView{}, errorOf(ErrNotReady, "%s is not ready", name)
	}

	view, err := m.recentMonitoring(ctx, inst)
	if err != nil {
		return MonitoringView{}, err
	}
	view.Provisioned = inst.Provisioned != ""
	view.Dashboards = dashboardViews()
	list, err := m.store.List()
	if err == nil {
		view.Setup = m.setupViews(list)
	}
	return view, nil
}

// recentMonitoring reads a monitoring service's Prometheus, or returns what
// it read moments ago.
func (m *Manager) recentMonitoring(ctx context.Context, inst Instance) (MonitoringView, error) {
	m.mon.mu.Lock()
	r, ok := m.mon.readings[inst.Name]
	m.mon.mu.Unlock()
	if ok && time.Since(r.at) <= monitoringTTL {
		return r.view, nil
	}
	vms, err := m.recentVMs(ctx)
	if err != nil {
		return MonitoringView{}, listError(err)
	}
	v, up := vms[primaryVM(inst)]
	if !up || v.Phase != "ready" || v.IP == "" {
		return MonitoringView{}, errorOf(ErrNotReady, "%s's VM is not up", inst.Name)
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	view, err := readMonitoring(readCtx, m.prom(v.IP), time.Now())
	if err != nil {
		return MonitoringView{}, errorOf(ErrNotReady, "%s's Prometheus does not answer: %v", inst.Name, err)
	}
	m.mon.mu.Lock()
	m.mon.readings[inst.Name] = monitoringReading{at: time.Now(), view: view}
	m.mon.mu.Unlock()
	return view, nil
}

// readMonitoring reads a Prometheus: its targets, rules, storage and the
// last hour's CPU by service.
func readMonitoring(ctx context.Context, p promClient, now time.Time) (MonitoringView, error) {
	v := MonitoringView{ReadAtUnix: now.Unix(), Watching: []WatchView{}, Rules: []RuleView{}, CPU: []SeriesView{}}

	var targets struct {
		Active []struct {
			Labels map[string]string `json:"labels"`
			Health string            `json:"health"`
		} `json:"activeTargets"`
	}
	if err := p.get(ctx, "/api/v1/targets", url.Values{"state": {"active"}}, &targets); err != nil {
		return v, err
	}
	type watch struct {
		recipe    string
		vms, jobs map[string]bool
		up, total int
	}
	byService := map[string]*watch{}
	allVMs := map[string]bool{}
	for _, t := range targets.Active {
		svc := t.Labels["service"]
		w := byService[svc]
		if w == nil {
			w = &watch{vms: map[string]bool{}, jobs: map[string]bool{}}
			byService[svc] = w
		}
		if r := t.Labels["recipe"]; r != "" {
			w.recipe = r
		}
		w.total++
		v.TargetsTotal++
		if t.Health == "up" {
			w.up++
			v.TargetsUp++
		}
		w.jobs[t.Labels["job"]] = true
		if t.Labels["job"] == "node" {
			w.vms[t.Labels["instance"]] = true
			allVMs[t.Labels["instance"]] = true
		}
	}
	for svc, w := range byService {
		jobs := make([]string, 0, len(w.jobs))
		for j := range w.jobs {
			jobs = append(jobs, j)
		}
		// The node exporter first, then the rest by name.
		sort.Slice(jobs, func(i, j int) bool {
			if (jobs[i] == "node") != (jobs[j] == "node") {
				return jobs[i] == "node"
			}
			return jobs[i] < jobs[j]
		})
		v.Watching = append(v.Watching, WatchView{Service: svc, Recipe: w.recipe, VMs: len(w.vms), Jobs: jobs, Up: w.up, Total: w.total})
	}
	sort.Slice(v.Watching, func(i, j int) bool { return v.Watching[i].Service < v.Watching[j].Service })
	v.VMs, v.Services = len(allVMs), len(v.Watching)

	var rules struct {
		Groups []struct {
			Rules []struct {
				Name        string            `json:"name"`
				Type        string            `json:"type"`
				State       string            `json:"state"`
				Annotations map[string]string `json:"annotations"`
				Alerts      []struct {
					State string `json:"state"`
				} `json:"alerts"`
			} `json:"rules"`
		} `json:"groups"`
	}
	if err := p.get(ctx, "/api/v1/rules", url.Values{"type": {"alert"}}, &rules); err != nil {
		return v, err
	}
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			if r.Type != "alerting" {
				continue
			}
			name := r.Annotations["title"]
			if name == "" {
				name = r.Name
			}
			state := r.State
			if state == "" || state == "inactive" {
				state = "ok"
			}
			for _, a := range r.Alerts {
				if a.State == "firing" {
					v.Firing++
				}
			}
			v.Rules = append(v.Rules, RuleView{Name: name, When: r.Annotations["when"], State: state})
		}
	}

	if stored, err := p.scalar(ctx, storedQuery); err == nil {
		v.StoredBytes = int64(stored)
	}
	var info struct {
		StorageRetention string `json:"storageRetention"`
	}
	if err := p.get(ctx, "/api/v1/status/runtimeinfo", nil, &info); err == nil {
		v.Retention = info.StorageRetention
	}
	if cpu, err := p.series(ctx, cpuQuery, "service", now.Add(-time.Hour), now, time.Minute); err == nil {
		v.CPU = cpu
	}
	return v, nil
}

// scalar runs an instant query and returns its first value.
func (p promClient) scalar(ctx context.Context, expr string) (float64, error) {
	var data struct {
		Result []struct {
			Value [2]any `json:"value"`
		} `json:"result"`
	}
	if err := p.get(ctx, "/api/v1/query", url.Values{"query": {expr}}, &data); err != nil {
		return 0, err
	}
	if len(data.Result) == 0 {
		return 0, errors.New("no result")
	}
	return sampleValue(data.Result[0].Value[1])
}

// series runs a range query and returns a series per value of label.
func (p promClient) series(ctx context.Context, expr, label string, start, end time.Time, step time.Duration) ([]SeriesView, error) {
	var data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	}
	q := url.Values{
		"query": {expr},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	}
	if err := p.get(ctx, "/api/v1/query_range", q, &data); err != nil {
		return nil, err
	}
	out := []SeriesView{}
	for _, r := range data.Result {
		s := SeriesView{Name: r.Metric[label], Points: [][2]float64{}}
		for _, pt := range r.Values {
			at, ok := pt[0].(float64)
			val, err := sampleValue(pt[1])
			if !ok || err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
				continue
			}
			s.Points = append(s.Points, [2]float64{at, math.Round(val*10) / 10})
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// sampleValue reads a sample value, which Prometheus writes as a string.
func sampleValue(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, errors.New("not a sample value")
	}
	return strconv.ParseFloat(s, 64)
}

// setupViews lists the exporters not yet set up on ready services' VMs:
// the one being set up, those waiting, and those that failed.
func (m *Manager) setupViews(list []Instance) []SetupView {
	m.mon.mu.Lock()
	defer m.mon.mu.Unlock()
	var out []SetupView
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for _, inst := range list {
		if inst.State != StateReady {
			continue
		}
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			continue
		}
		for _, mem := range inst.members() {
			for _, e := range exportersOn(recipe, inst, mem) {
				key := watchKey(mem.VM, e.Job)
				if slices.Contains(inst.Watched, key) {
					continue
				}
				s := SetupView{Service: inst.Name, VM: mem.VM, What: e.Label, Running: m.mon.current == key}
				if f, ok := m.mon.failed[key]; ok && !s.Running {
					s.Problem = truncate(f.msg, 300)
				}
				out = append(out, s)
			}
		}
	}
	return out
}
