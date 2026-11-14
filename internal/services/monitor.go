package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// Monitoring. Once a monitoring service (Prometheus + Grafana) is ready, the
// daemon sets up exporters on every service's VMs over SSH, keeps each
// Prometheus's file_sd targets current, installs its own configuration, alert
// rules and dashboards, and reads firing alerts back into the activity feed.
// Everything is pushed from the daemon, so the VMs never need to reach it.

// consoleMonitoring is the console of a service that watches the others.
const consoleMonitoring = "monitoring"

//go:embed monitoring
var monitoringFS embed.FS

func monitoringFile(name string) string {
	b, err := monitoringFS.ReadFile("monitoring/" + name)
	if err != nil {
		panic(err) // embedded at build time: a missing file is a build mistake
	}
	return string(b)
}

// exporter is a Prometheus exporter nano-cloud sets up on service VMs.
type exporter struct {
	// Job is the scrape job name.
	Job string
	// Label is shown in the console.
	Label string
	Port  int
	// Once limits it to the first VM of its role, for exporters that report
	// a whole cluster.
	Once   bool
	script string
}

// nodeExporter is every VM's own: CPU, memory, disks and network.
const nodeExporter = "node"

var exporters = map[string]exporter{
	nodeExporter:         {Job: "node", Label: "VM metrics", Port: 9100, script: "node.sh"},
	"postgres":           {Job: "postgres", Label: "PostgreSQL metrics", Port: 9187, script: "postgres.sh"},
	"mysql":              {Job: "mysql", Label: "MySQL metrics", Port: 9104, script: "mysql.sh"},
	"kube_state_metrics": {Job: "kube-state-metrics", Label: "Kubernetes metrics", Port: 32080, Once: true, script: "kube-state-metrics.sh"},
}

// exportersOn returns the exporters a service's VM gets, node exporter first.
func exportersOn(recipe Recipe, inst Instance, mem Member) []exporter {
	out := []exporter{exporters[nodeExporter]}
	for _, mt := range recipe.Metrics {
		if mt.Role != "" && mt.Role != mem.Role {
			continue
		}
		e := exporters[mt.Exporter]
		if e.Once && firstOfRole(inst, mem.Role) != mem.VM {
			continue
		}
		out = append(out, e)
	}
	return out
}

func firstOfRole(inst Instance, role string) string {
	for _, mem := range inst.members() {
		if mem.Role == role {
			return mem.VM
		}
	}
	return ""
}

// watchKey names one exporter on one VM, as Instance.Watched records it.
func watchKey(vm, job string) string { return vm + "/" + job }

// unwatch forgets a VM's exporters, for a VM that is gone or made anew.
func unwatch(inst *Instance, vm string) {
	inst.Watched = slices.DeleteFunc(inst.Watched, func(k string) bool {
		return len(k) > len(vm) && k[:len(vm)+1] == vm+"/"
	})
}

func setupCmd(e exporter) string {
	return "sudo -n bash -c " + shellQuote(monitoringFile("prelude.sh")+monitoringFile(e.script))
}

// targetsCmd atomically replaces the targets file Prometheus watches with
// standard input.
const targetsCmd = "sudo -n sh -c 'cat > /etc/prometheus/nanocloud.d/.targets.tmp && " +
	"mv /etc/prometheus/nanocloud.d/.targets.tmp /etc/prometheus/nanocloud.d/targets.json'"

// setupTimeout bounds one exporter's setup: an apt install, or waiting for
// kube-state-metrics' image to come down.
const setupTimeout = 10 * time.Minute

// monitorState is what the monitor keeps between looks.
type monitorState struct {
	mu sync.Mutex
	// running is set while a batch of setups runs; current is the one in
	// progress.
	running bool
	current string
	// failed holds failed setups by watch key and failed provisioning by
	// "provision/<service>"; each is retried after setupRetry.
	failed map[string]setupFailure
	// pushed is the hash of the targets each monitoring service has.
	pushed map[string]string
	// lastIP is each VM's last address, so a VM that goes down stays a
	// target and its alert fires.
	lastIP map[string]string
	// firing holds the firing alerts, by key.
	firing map[string]Alert
	// readings are recent monitoring views, by service.
	readings map[string]monitoringReading
}

type setupFailure struct {
	at  time.Time
	msg string
}

type monitoringReading struct {
	at   time.Time
	view MonitoringView
}

func newMonitorState() monitorState {
	return monitorState{
		failed:   map[string]setupFailure{},
		pushed:   map[string]string{},
		lastIP:   map[string]string{},
		firing:   map[string]Alert{},
		readings: map[string]monitoringReading{},
	}
}

// keepMonitoring runs monitorOnce every monitorEvery until the manager stops.
func (m *Manager) keepMonitoring() {
	defer m.wg.Done()
	t := time.NewTicker(m.monitorEvery)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.monitorOnce(m.ctx)
		}
	}
}

// monitorHost is a monitoring service that is up, with its VM's address.
type monitorHost struct {
	inst   Instance
	vm, ip string
}

// monitorOnce sets up missing exporters, then updates each monitoring
// service's configuration and targets and reads its alerts.
func (m *Manager) monitorOnce(ctx context.Context) {
	list, err := m.store.List()
	if err != nil {
		return
	}
	vms, err := m.recentVMs(ctx)
	if err != nil {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	m.mon.mu.Lock()
	for name, v := range vms {
		if v.IP != "" {
			m.mon.lastIP[name] = v.IP
		}
	}
	m.mon.mu.Unlock()

	var hosts []monitorHost
	for _, inst := range list {
		recipe, _ := RecipeByID(inst.Recipe)
		if recipe.Console != consoleMonitoring || inst.State != StateReady {
			continue
		}
		vm := primaryVM(inst)
		if v, ok := vms[vm]; ok && v.Phase == "ready" && v.IP != "" {
			hosts = append(hosts, monitorHost{inst: inst, vm: vm, ip: v.IP})
		}
	}
	if len(hosts) == 0 {
		// Nothing watches: no alerts to show, and none resolved either.
		m.mon.mu.Lock()
		m.mon.firing = map[string]Alert{}
		m.mon.mu.Unlock()
		return
	}

	m.startSetups(list, vms)
	targets := m.targets(list, vms)
	read := map[string][]Alert{}
	var unread []string
	for _, h := range hosts {
		if err := m.provision(ctx, h); err != nil {
			unread = append(unread, h.inst.Name)
			continue
		}
		m.pushTargets(ctx, h, targets)
		alerts, err := m.prom(h.ip).alerts(ctx)
		if err != nil {
			unread = append(unread, h.inst.Name)
			continue
		}
		read[h.inst.Name] = alerts
	}
	m.updateAlerts(read, unread)
}

// setupJob is one exporter to set up on one VM.
type setupJob struct {
	service, vm, ip, hostKey string
	exp                      exporter
}

// startSetups sets up, in the background, every exporter a ready service's
// VM should have and does not, unless a batch is already under way.
func (m *Manager) startSetups(list []Instance, vms map[string]vmView) {
	m.mon.mu.Lock()
	defer m.mon.mu.Unlock()
	if m.mon.running {
		return
	}
	var todo []setupJob
	for _, inst := range list {
		if inst.State != StateReady {
			continue
		}
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			continue
		}
		for _, mem := range inst.members() {
			v, up := vms[mem.VM]
			if !up || v.Phase != "ready" || v.IP == "" {
				continue
			}
			for _, e := range exportersOn(recipe, inst, mem) {
				key := watchKey(mem.VM, e.Job)
				if slices.Contains(inst.Watched, key) {
					continue
				}
				if f, ok := m.mon.failed[key]; ok && time.Since(f.at) < m.setupRetry {
					continue
				}
				todo = append(todo, setupJob{service: inst.Name, vm: mem.VM, ip: v.IP, hostKey: inst.hostKey(mem.VM), exp: e})
			}
		}
	}
	if len(todo) == 0 {
		return
	}
	m.mon.running = true
	m.wg.Add(1)
	go m.runSetups(todo)
}

// runSetups sets the exporters up one after another, recording each that
// worked on its service so it is a target from then on.
func (m *Manager) runSetups(todo []setupJob) {
	defer m.wg.Done()
	defer func() {
		m.mon.mu.Lock()
		m.mon.running, m.mon.current = false, ""
		m.mon.mu.Unlock()
	}()
	for _, j := range todo {
		key := watchKey(j.vm, j.exp.Job)
		m.mon.mu.Lock()
		m.mon.current = key
		m.mon.mu.Unlock()

		ctx, cancel := context.WithTimeout(m.ctx, setupTimeout)
		_, _, err := m.guest.Run(ctx, j.ip, j.hostKey, setupCmd(j.exp))
		cancel()
		if m.ctx.Err() != nil {
			return
		}
		m.mon.mu.Lock()
		if err != nil {
			m.mon.failed[key] = setupFailure{at: time.Now(), msg: err.Error()}
		} else {
			delete(m.mon.failed, key)
		}
		m.mon.mu.Unlock()
		if err != nil {
			log.Printf("services: setting up %s on %s: %v", j.exp.Label, j.vm, err)
			continue
		}
		// A service deleted meanwhile has nothing to record it on.
		_ = m.store.Update(j.service, func(i *Instance) {
			if !slices.Contains(i.Watched, key) {
				i.Watched = append(i.Watched, key)
			}
		})
	}
}

// targetGroup is one entry of a Prometheus file_sd file.
type targetGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// targets are what Prometheus should scrape: every exporter set up on a
// ready service's running VMs. A stopped VM is not a target, so stopping a
// service raises no alert, while a running VM that stops answering does.
func (m *Manager) targets(list []Instance, vms map[string]vmView) []targetGroup {
	m.mon.mu.Lock()
	defer m.mon.mu.Unlock()
	out := []targetGroup{}
	for _, inst := range list {
		if inst.State != StateReady {
			continue
		}
		recipe, ok := RecipeByID(inst.Recipe)
		if !ok {
			continue
		}
		for _, mem := range inst.members() {
			ip := m.mon.lastIP[mem.VM]
			if v, ok := vms[mem.VM]; !ok || v.Status != "running" || ip == "" {
				continue
			}
			for _, e := range exportersOn(recipe, inst, mem) {
				if !slices.Contains(inst.Watched, watchKey(mem.VM, e.Job)) {
					continue
				}
				out = append(out, targetGroup{
					Targets: []string{net.JoinHostPort(ip, strconv.Itoa(e.Port))},
					Labels: map[string]string{
						"job":      e.Job,
						"instance": mem.VM,
						"service":  inst.Name,
						"recipe":   inst.Recipe,
						"role":     mem.Role,
					},
				})
			}
		}
	}
	return out
}

// pushTargets gives a monitoring service's Prometheus the targets, when
// they differ from what it has.
func (m *Manager) pushTargets(ctx context.Context, h monitorHost, targets []targetGroup) {
	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return
	}
	sum := hashOf(data)
	m.mon.mu.Lock()
	same := m.mon.pushed[h.inst.Name] == sum
	m.mon.mu.Unlock()
	if same {
		return
	}
	if _, _, err := m.guest.Send(ctx, h.ip, h.inst.hostKey(h.vm), targetsCmd, append(data, '\n')); err != nil {
		log.Printf("services: telling %s what to watch: %v", h.inst.Name, err)
		return
	}
	m.mon.mu.Lock()
	m.mon.pushed[h.inst.Name] = sum
	m.mon.mu.Unlock()
}

// provision installs nano-cloud's Prometheus configuration, alert rules and
// dashboards on a monitoring service unless it already has this version.
// Failures are retried after setupRetry.
func (m *Manager) provision(ctx context.Context, h monitorHost) error {
	bundle, sum, err := monitoringBundle(h.inst.Name, h.vm)
	if err != nil {
		return err
	}
	if h.inst.Provisioned == sum {
		return nil
	}
	key := "provision/" + h.inst.Name
	m.mon.mu.Lock()
	f, failed := m.mon.failed[key]
	m.mon.mu.Unlock()
	if failed && time.Since(f.at) < m.setupRetry {
		return fmt.Errorf("%s", f.msg)
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, _, err = m.guest.Send(ctx, h.ip, h.inst.hostKey(h.vm), "sudo -n bash -c "+shellQuote(monitoringFile("provision.sh")), bundle)
	m.mon.mu.Lock()
	if err != nil {
		m.mon.failed[key] = setupFailure{at: time.Now(), msg: err.Error()}
	} else {
		delete(m.mon.failed, key)
		// Prometheus restarted with the new configuration; push the
		// targets again.
		delete(m.mon.pushed, h.inst.Name)
	}
	m.mon.mu.Unlock()
	if err != nil {
		log.Printf("services: setting up monitoring on %s: %v", h.inst.Name, err)
		return err
	}
	first := h.inst.Provisioned == ""
	if err := m.store.Update(h.inst.Name, func(i *Instance) { i.Provisioned = sum }); err != nil {
		return err
	}
	if first {
		m.note(h.inst.Name, fmt.Sprintf("%s now watches every service: dashboards and alert rules are set up.", h.inst.Name))
	}
	return nil
}

// prometheusConfig is the configuration nano-cloud gives a monitoring
// service's Prometheus. Relative paths are to its own directory.
func prometheusConfig(service, vm string) string {
	return fmt.Sprintf(`# Written by nano-cloud, which replaces it whenever it updates its
# monitoring. What to scrape is in nanocloud.d/, kept up to date by
# nano-cloud as services come and go. The configuration this replaced is
# in prometheus.yml.before-nanocloud.
global:
  scrape_interval: 15s
  evaluation_interval: 15s
rule_files:
  - nanocloud-rules.yml
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: [localhost:9090]
        labels: {service: %q, instance: %q}
  - job_name: nanocloud
    file_sd_configs:
      - files: [nanocloud.d/*.json]
`, service, vm)
}

const grafanaProvider = `# Written by nano-cloud: its dashboards, replaced when nano-cloud updates them.
apiVersion: 1
providers:
  - name: nano-cloud
    folder: nano-cloud
    type: file
    disableDeletion: true
    allowUiUpdates: false
    updateIntervalSeconds: 60
    options:
      path: /var/lib/grafana/dashboards/nanocloud
`

// monitoringBundle is everything provision installs, as a gzipped tar of
// paths relative to /, with a hash of its contents.
func monitoringBundle(service, vm string) ([]byte, string, error) {
	files := map[string]string{
		"etc/prometheus/prometheus.yml":                      prometheusConfig(service, vm),
		"etc/prometheus/nanocloud-rules.yml":                 monitoringFile("rules.yml"),
		"etc/grafana/provisioning/dashboards/nanocloud.yaml": grafanaProvider,
	}
	for _, d := range dashboards() {
		data, err := json.MarshalIndent(d.board, "", "  ")
		if err != nil {
			return nil, "", err
		}
		files["var/lib/grafana/dashboards/nanocloud/"+d.file] = string(data) + "\n"
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	h := sha256.New()
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, p := range paths {
		body := files[p]
		h.Write([]byte(p + "\x00" + body + "\x00"))
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil))[:16], nil
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:8])
}

// Alert is an alert firing in a monitoring service's Prometheus.
type Alert struct {
	// Name is the rule name (DiskFillingUp); Title is the readable one.
	Name     string `json:"name"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Severity string `json:"severity,omitempty"`
	// Service and VM are what it is about.
	Service   string `json:"service,omitempty"`
	VM        string `json:"vm,omitempty"`
	SinceUnix int64  `json:"since_unix,omitempty"`
	// key tells one alert from another; monitor is the service that fired it.
	key, monitor string
}

// updateAlerts records the alerts each monitoring service reported and adds
// those that started or ended since the last look to the feed. Alerts from a
// service that could not be read are assumed to be still firing.
func (m *Manager) updateAlerts(read map[string][]Alert, unread []string) {
	m.mon.mu.Lock()
	prev := m.mon.firing
	cur := map[string]Alert{}
	for monitor, list := range read {
		for _, a := range list {
			if _, dup := cur[a.key]; !dup {
				a.monitor = monitor
				cur[a.key] = a
			}
		}
	}
	for k, a := range prev {
		if _, ok := cur[k]; !ok && slices.Contains(unread, a.monitor) {
			cur[k] = a
		}
	}
	m.mon.firing = cur
	m.mon.mu.Unlock()

	for _, k := range sortedKeys(cur) {
		if _, ok := prev[k]; !ok {
			m.alertEvent(cur[k], true)
		}
	}
	for _, k := range sortedKeys(prev) {
		if _, ok := cur[k]; !ok {
			m.alertEvent(prev[k], false)
		}
	}
}

func sortedKeys(alerts map[string]Alert) []string {
	keys := make([]string, 0, len(alerts))
	for k := range alerts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// alertEvent adds an alert starting or ending to the feed, under the
// service it is about.
func (m *Manager) alertEvent(a Alert, firing bool) {
	where := a.Service
	if where == "" {
		where = a.monitor
	}
	ev := events.Event{Kind: events.KindServiceAlert, VM: where, Detail: "Alert " + a.Name + " from " + a.monitor + "'s Prometheus"}
	if firing {
		ev.Level = events.LevelWarn
		if a.Severity == "critical" {
			ev.Level = events.LevelError
		}
		ev.Message = sentence(a.Title + ": " + a.Summary)
	} else {
		ev.Level = events.LevelInfo
		ev.Message = sentence("Resolved: " + a.Title + ": " + a.Summary)
	}
	m.events.Append(ev)
}

// alertsOn returns the alerts firing about a service.
func (m *Manager) alertsOn(name string) []Alert {
	m.mon.mu.Lock()
	defer m.mon.mu.Unlock()
	var out []Alert
	for _, k := range sortedKeys(m.mon.firing) {
		if a := m.mon.firing[k]; a.Service == name {
			out = append(out, a)
		}
	}
	return out
}

// forgetMonitoring drops what the monitor holds for a deleted service.
func (m *Manager) forgetMonitoring(name string) {
	m.mon.mu.Lock()
	defer m.mon.mu.Unlock()
	delete(m.mon.pushed, name)
	delete(m.mon.readings, name)
	delete(m.mon.failed, "provision/"+name)
}
