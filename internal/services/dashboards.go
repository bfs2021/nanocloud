package services

// Grafana dashboards nano-cloud installs on a monitoring service. They are
// built in code rather than kept as exported JSON so the tests can check every
// query. Panels select and group by the target labels service, instance and
// job.

// board is one dashboard and where it is installed.
type board struct {
	file  string
	view  DashboardView
	board map[string]any
}

func dashboards() []board {
	return []board{overviewBoard(), nodeBoard(), kubernetesBoard(), postgresBoard(), mysqlBoard(), prometheusBoard()}
}

// dashboardViews describes the dashboards for the console.
func dashboardViews() []DashboardView {
	var out []DashboardView
	for _, d := range dashboards() {
		out = append(out, d.view)
	}
	return out
}

// ds is the data source every panel uses: the dashboard's datasource
// variable, which resolves to the default Prometheus whatever its uid.
var ds = map[string]any{"type": "prometheus", "uid": "${datasource}"}

// query is one PromQL query of a panel.
type query struct {
	expr, legend string
	// table is an instant query in table format, for table panels.
	table bool
}

func q(expr, legend string) query { return query{expr: expr, legend: legend} }

func targets(qs []query) []map[string]any {
	out := make([]map[string]any, 0, len(qs))
	for i, x := range qs {
		t := map[string]any{"refId": string(rune('A' + i)), "expr": x.expr, "datasource": ds}
		if x.legend != "" {
			t["legendFormat"] = x.legend
		}
		if x.table {
			t["instant"], t["range"], t["format"] = true, false, "table"
		}
		out = append(out, t)
	}
	return out
}

// layout places panels left to right on Grafana's 24-column grid.
type layout struct {
	x, y, rowH, id int
	panels         []map[string]any
}

func (l *layout) add(w, h int, p map[string]any) {
	if l.x+w > 24 {
		l.x, l.y, l.rowH = 0, l.y+l.rowH, 0
	}
	l.id++
	p["id"] = l.id
	p["gridPos"] = map[string]int{"x": l.x, "y": l.y, "w": w, "h": h}
	p["datasource"] = ds
	l.panels = append(l.panels, p)
	l.x += w
	if h > l.rowH {
		l.rowH = h
	}
}

func timeseries(title, unit string, qs ...query) map[string]any {
	return map[string]any{
		"type":  "timeseries",
		"title": title,
		"fieldConfig": map[string]any{
			"defaults": map[string]any{
				"unit":   unit,
				"custom": map[string]any{"lineWidth": 2, "fillOpacity": 10, "showPoints": "never", "spanNulls": true},
			},
			"overrides": []any{},
		},
		"options": map[string]any{
			"legend":  map[string]any{"displayMode": "list", "placement": "bottom", "showLegend": true},
			"tooltip": map[string]any{"mode": "multi", "sort": "desc"},
		},
		"targets": targets(qs),
	}
}

// stacked is a timeseries whose series add up, such as CPU by mode.
func stacked(title, unit string, qs ...query) map[string]any {
	p := timeseries(title, unit, qs...)
	custom := p["fieldConfig"].(map[string]any)["defaults"].(map[string]any)["custom"].(map[string]any)
	custom["stacking"] = map[string]any{"mode": "normal"}
	custom["fillOpacity"] = 25
	return p
}

// stat is one number. When above is set, the value turns red once it
// reaches it.
func stat(title, unit string, x query, above ...float64) map[string]any {
	steps := []any{map[string]any{"color": "text", "value": nil}}
	if len(above) > 0 {
		steps = append(steps, map[string]any{"color": "red", "value": above[0]})
	}
	return map[string]any{
		"type":  "stat",
		"title": title,
		"fieldConfig": map[string]any{
			"defaults": map[string]any{
				"unit":       unit,
				"thresholds": map[string]any{"mode": "absolute", "steps": steps},
				"color":      map[string]any{"mode": "thresholds"},
			},
			"overrides": []any{},
		},
		"options": map[string]any{
			"reduceOptions": map[string]any{"calcs": []string{"lastNotNull"}, "fields": "", "values": false},
			"colorMode":     "value",
			"graphMode":     "area",
			"textMode":      "value",
			"justifyMode":   "auto",
		},
		"targets": targets([]query{x}),
	}
}

// upStat shows whether something answers: up or down, in words and colour.
func upStat(title string, x query) map[string]any {
	p := stat(title, "none", x)
	p["fieldConfig"].(map[string]any)["defaults"].(map[string]any)["mappings"] = upMappings
	p["options"].(map[string]any)["graphMode"] = "none"
	return p
}

var upMappings = []any{map[string]any{
	"type": "value",
	"options": map[string]any{
		"0": map[string]any{"text": "down", "color": "red", "index": 0},
		"1": map[string]any{"text": "up", "color": "green", "index": 1},
	},
}}

// column says how a table shows one query's values.
type column struct {
	name, unit string
	gauge, up  bool
}

// labelColumn is a table column of a label's values, which may link on.
type labelColumn struct {
	label, name, link string
}

// table joins instant queries on their labels into one row per label set:
// a column per label, then one per query.
func table(title string, labels []labelColumn, cols []column, qs ...query) map[string]any {
	rename := map[string]string{}
	order := map[string]int{}
	var overrides []any
	for i, lc := range labels {
		rename[lc.label] = lc.name
		order[lc.label] = i
		if lc.link != "" {
			overrides = append(overrides, map[string]any{
				"matcher": map[string]any{"id": "byName", "options": lc.name},
				"properties": []any{map[string]any{"id": "links", "value": []any{
					map[string]any{"title": "Details", "url": lc.link},
				}}},
			})
		}
	}
	for n := range qs {
		qs[n].table = true
		field := "Value #" + string(rune('A'+n))
		rename[field] = cols[n].name
		order[field] = len(labels) + n
		props := []any{}
		if cols[n].unit != "" {
			props = append(props, map[string]any{"id": "unit", "value": cols[n].unit})
		}
		if cols[n].gauge {
			props = append(props,
				map[string]any{"id": "custom.cellOptions", "value": map[string]any{"type": "gauge", "mode": "basic"}},
				map[string]any{"id": "min", "value": 0},
				map[string]any{"id": "max", "value": 100},
				map[string]any{"id": "thresholds", "value": map[string]any{"mode": "absolute", "steps": []any{
					map[string]any{"color": "blue", "value": nil},
					map[string]any{"color": "orange", "value": 80},
					map[string]any{"color": "red", "value": 90},
				}}})
		}
		if cols[n].up {
			props = append(props,
				map[string]any{"id": "mappings", "value": upMappings},
				map[string]any{"id": "custom.cellOptions", "value": map[string]any{"type": "color-text"}})
		}
		overrides = append(overrides, map[string]any{"matcher": map[string]any{"id": "byName", "options": cols[n].name}, "properties": props})
	}
	return map[string]any{
		"type":  "table",
		"title": title,
		"fieldConfig": map[string]any{
			"defaults":  map[string]any{"custom": map[string]any{"align": "auto", "cellOptions": map[string]any{"type": "auto"}}},
			"overrides": overrides,
		},
		"options": map[string]any{"showHeader": true, "cellHeight": "sm"},
		"transformations": []any{
			map[string]any{"id": "merge", "options": map[string]any{}},
			map[string]any{"id": "organize", "options": map[string]any{
				"excludeByName": map[string]bool{"Time": true},
				"renameByName":  rename,
				"indexByName":   order,
			}},
		},
		"targets": targets(qs),
	}
}

// variable is a dashboard variable listing a label's values, such as the
// VMs to pick one from.
func variable(name, label, def string) map[string]any {
	return map[string]any{
		"name":       name,
		"label":      label,
		"type":       "query",
		"datasource": ds,
		"query":      def,
		"definition": def,
		"refresh":    2,
		"sort":       1,
		"current":    map[string]any{},
	}
}

func dashboard(view DashboardView, vars []map[string]any, l *layout) map[string]any {
	list := []any{map[string]any{
		"name":    "datasource",
		"label":   "Data source",
		"type":    "datasource",
		"query":   "prometheus",
		"hide":    2,
		"current": map[string]any{},
	}}
	for _, v := range vars {
		list = append(list, v)
	}
	return map[string]any{
		"uid":           view.UID,
		"title":         view.Title,
		"description":   view.About,
		"tags":          []string{"nano-cloud"},
		"editable":      false,
		"schemaVersion": 39,
		"version":       1,
		"time":          map[string]any{"from": "now-1h", "to": "now"},
		"refresh":       "30s",
		"timezone":      "browser",
		"graphTooltip":  1,
		"templating":    map[string]any{"list": list},
		"panels":        l.panels,
		"links": []any{map[string]any{
			"type": "dashboards", "tags": []string{"nano-cloud"}, "asDropdown": true,
			"title": "nano-cloud", "keepTime": true,
		}},
	}
}

func overviewBoard() board {
	v := DashboardView{UID: "nanocloud-overview", Title: "nano-cloud overview", About: "Every VM, grouped by service: up, CPU, memory, disk, network", Home: true}
	l := &layout{}
	l.add(6, 4, stat("Services watched", "none", q(`count(count by (service) (up{job="node"}))`, "")))
	l.add(6, 4, stat("VMs watched", "none", q(`count(up{job="node"})`, "")))
	l.add(6, 4, stat("VMs not answering", "none", q(`count(up{job="node"} == 0) or vector(0)`, ""), 1))
	l.add(6, 4, stat("Alerts firing", "none", q(`count(ALERTS{alertstate="firing"}) or vector(0)`, ""), 1))
	l.add(12, 8, timeseries("CPU by service", "percent",
		q(`100 * (1 - avg by (service) (rate(node_cpu_seconds_total{mode="idle"}[$__rate_interval])))`, "{{service}}")))
	l.add(12, 8, timeseries("Memory by service", "percent",
		q(`100 * (1 - sum by (service) (node_memory_MemAvailable_bytes) / sum by (service) (node_memory_MemTotal_bytes))`, "{{service}}")))
	l.add(12, 8, timeseries("Network by service", "Bps",
		q(`sum by (service) (rate(node_network_receive_bytes_total{device!~"`+virtualNICs+`"}[$__rate_interval]) + rate(node_network_transmit_bytes_total{device!~"`+virtualNICs+`"}[$__rate_interval]))`, "{{service}}")))
	l.add(12, 8, timeseries("Fullest disk by service", "percent",
		q(`max by (service) (100 * (1 - node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"}))`, "{{service}}")))
	l.add(24, 10, table("Every VM",
		[]labelColumn{
			{label: "service", name: "Service"},
			{label: "instance", name: "VM", link: "/d/nanocloud-node?var-vm=${__value.raw}&${__url_time_range}"},
		},
		[]column{
			{name: "Answering", up: true},
			{name: "CPU", unit: "percent", gauge: true},
			{name: "Memory", unit: "percent", gauge: true},
			{name: "Disk", unit: "percent", gauge: true},
			{name: "Up for", unit: "s"},
		},
		q(`max by (service, instance) (up{job="node"})`, ""),
		q(`100 * (1 - avg by (service, instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])))`, ""),
		q(`100 * (1 - max by (service, instance) (node_memory_MemAvailable_bytes) / max by (service, instance) (node_memory_MemTotal_bytes))`, ""),
		q(`100 * (1 - max by (service, instance) (node_filesystem_avail_bytes{mountpoint="/"}) / max by (service, instance) (node_filesystem_size_bytes{mountpoint="/"}))`, ""),
		q(`max by (service, instance) (time() - node_boot_time_seconds)`, ""),
	))
	return board{file: "overview.json", view: v, board: dashboard(v, nil, l)}
}

// virtualNICs are the interfaces Kubernetes adds on a node, whose traffic
// the VM's own interface carries too.
const virtualNICs = "lo|veth.*|cni.*|flannel.*|cali.*"

func nodeBoard() board {
	v := DashboardView{UID: "nanocloud-node", Title: "Node details", About: "One VM in depth: CPU by mode, memory, disk I/O, filesystems, network", Var: "vm"}
	sel := `instance="$vm"`
	l := &layout{}
	l.add(6, 4, upStat("Answering", q(`up{job="node",`+sel+`}`, "")))
	l.add(6, 4, stat("Up for", "s", q(`time() - node_boot_time_seconds{`+sel+`}`, "")))
	l.add(6, 4, stat("CPUs", "none", q(`count(node_cpu_seconds_total{`+sel+`,mode="idle"})`, "")))
	l.add(6, 4, stat("Memory", "bytes", q(`node_memory_MemTotal_bytes{`+sel+`}`, "")))
	l.add(12, 8, stacked("CPU by mode", "percent",
		q(`100 * sum by (mode) (rate(node_cpu_seconds_total{`+sel+`,mode!="idle"}[$__rate_interval])) / scalar(count(node_cpu_seconds_total{`+sel+`,mode="idle"}))`, "{{mode}}")))
	l.add(12, 8, timeseries("Memory", "bytes",
		q(`node_memory_MemTotal_bytes{`+sel+`} - node_memory_MemAvailable_bytes{`+sel+`}`, "used"),
		q(`node_memory_Cached_bytes{`+sel+`} + node_memory_Buffers_bytes{`+sel+`}`, "cache and buffers"),
		q(`node_memory_MemAvailable_bytes{`+sel+`}`, "available")))
	l.add(12, 8, timeseries("Disk I/O", "Bps",
		q(`sum(rate(node_disk_read_bytes_total{`+sel+`}[$__rate_interval]))`, "read"),
		q(`sum(rate(node_disk_written_bytes_total{`+sel+`}[$__rate_interval]))`, "written")))
	l.add(12, 8, timeseries("Filesystems used", "percent",
		q(`100 * (1 - node_filesystem_avail_bytes{`+sel+`,fstype!~"tmpfs|ramfs|squashfs|overlay"} / node_filesystem_size_bytes{`+sel+`,fstype!~"tmpfs|ramfs|squashfs|overlay"})`, "{{mountpoint}}")))
	l.add(12, 8, timeseries("Network", "Bps",
		q(`sum(rate(node_network_receive_bytes_total{`+sel+`,device!~"`+virtualNICs+`"}[$__rate_interval]))`, "received"),
		q(`sum(rate(node_network_transmit_bytes_total{`+sel+`,device!~"`+virtualNICs+`"}[$__rate_interval]))`, "sent")))
	l.add(12, 8, timeseries("Load", "none",
		q(`node_load1{`+sel+`}`, "1 min"),
		q(`node_load5{`+sel+`}`, "5 min"),
		q(`node_load15{`+sel+`}`, "15 min")))
	return board{file: "node.json", view: v, board: dashboard(v, []map[string]any{variable("vm", "VM", `label_values(node_uname_info, instance)`)}, l)}
}

func kubernetesBoard() board {
	v := DashboardView{UID: "nanocloud-kubernetes", Title: "Kubernetes", About: "Nodes, pods by namespace, restarts, deployments", Var: "cluster", Recipes: []string{"kubernetes", "kubernetes_cluster"}}
	sel := `service="$cluster"`
	l := &layout{}
	l.add(6, 4, stat("Nodes ready", "none", q(`sum(kube_node_status_condition{`+sel+`,condition="Ready",status="true"})`, "")))
	l.add(6, 4, stat("Pods running", "none", q(`sum(kube_pod_status_phase{`+sel+`,phase="Running"})`, "")))
	l.add(6, 4, stat("Pods not running", "none", q(`sum(kube_pod_status_phase{`+sel+`,phase=~"Pending|Failed|Unknown"}) or vector(0)`, ""), 1))
	l.add(6, 4, stat("Restarts, last hour", "none", q(`sum(increase(kube_pod_container_status_restarts_total{`+sel+`}[1h])) or vector(0)`, ""), 1))
	l.add(12, 8, timeseries("Pods running by namespace", "none",
		q(`sum by (namespace) (kube_pod_status_phase{`+sel+`,phase="Running"})`, "{{namespace}}")))
	l.add(12, 8, timeseries("Restarts in 15 minutes, by pod", "none",
		q(`topk(10, sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{`+sel+`}[15m])) > 0)`, "{{namespace}}/{{pod}}")))
	l.add(12, 8, timeseries("CPU by node", "percent",
		q(`100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{`+sel+`,mode="idle"}[$__rate_interval])))`, "{{instance}}")))
	l.add(12, 8, timeseries("Memory by node", "percent",
		q(`100 * (1 - node_memory_MemAvailable_bytes{`+sel+`} / node_memory_MemTotal_bytes{`+sel+`})`, "{{instance}}")))
	l.add(24, 9, table("Deployments",
		[]labelColumn{{label: "namespace", name: "Namespace"}, {label: "deployment", name: "Deployment"}},
		[]column{{name: "Available"}, {name: "Wanted"}},
		q(`max by (namespace, deployment) (kube_deployment_status_replicas_available{`+sel+`})`, ""),
		q(`max by (namespace, deployment) (kube_deployment_spec_replicas{`+sel+`})`, ""),
	))
	return board{file: "kubernetes.json", view: v, board: dashboard(v, []map[string]any{variable("cluster", "Cluster", `label_values(kube_node_info, service)`)}, l)}
}

func postgresBoard() board {
	v := DashboardView{UID: "nanocloud-postgres", Title: "PostgreSQL", About: "Connections, transactions per second, cache hit ratio, database size", Var: "db", Recipes: []string{"postgres"}}
	sel := `service="$db"`
	rate := func(m string) string { return `rate(` + m + `{` + sel + `}[$__rate_interval])` }
	l := &layout{}
	l.add(6, 4, upStat("Answering", q(`max(pg_up{`+sel+`})`, "")))
	l.add(6, 4, stat("Connections", "none", q(`sum(pg_stat_database_numbackends{`+sel+`})`, "")))
	l.add(6, 4, stat("Size of its databases", "bytes", q(`sum(pg_database_size_bytes{`+sel+`})`, "")))
	l.add(6, 4, stat("Cache hit ratio", "percentunit", q(`sum(rate(pg_stat_database_blks_hit{`+sel+`}[5m])) / clamp_min(sum(rate(pg_stat_database_blks_hit{`+sel+`}[5m])) + sum(rate(pg_stat_database_blks_read{`+sel+`}[5m])), 1e-9)`, "")))
	l.add(12, 8, timeseries("Transactions per second", "ops",
		q(`sum by (datname) (`+rate("pg_stat_database_xact_commit")+` + `+rate("pg_stat_database_xact_rollback")+`)`, "{{datname}}")))
	l.add(12, 8, timeseries("Connections by database", "none",
		q(`sum by (datname) (pg_stat_database_numbackends{`+sel+`})`, "{{datname}}")))
	l.add(12, 8, timeseries("Rows per second", "ops",
		q(`sum(`+rate("pg_stat_database_tup_fetched")+`)`, "read"),
		q(`sum(`+rate("pg_stat_database_tup_inserted")+`)`, "inserted"),
		q(`sum(`+rate("pg_stat_database_tup_updated")+`)`, "updated"),
		q(`sum(`+rate("pg_stat_database_tup_deleted")+`)`, "deleted")))
	l.add(12, 8, timeseries("Database size", "bytes",
		q(`pg_database_size_bytes{`+sel+`}`, "{{datname}}")))
	return board{file: "postgres.json", view: v, board: dashboard(v, []map[string]any{variable("db", "Database service", `label_values(pg_up, service)`)}, l)}
}

func mysqlBoard() board {
	v := DashboardView{UID: "nanocloud-mysql", Title: "MySQL", About: "Connections, queries per second, slow queries, InnoDB buffer pool", Var: "db", Recipes: []string{"mysql"}}
	sel := `service="$db"`
	l := &layout{}
	l.add(6, 4, upStat("Answering", q(`max(mysql_up{`+sel+`})`, "")))
	l.add(6, 4, stat("Connections", "none", q(`sum(mysql_global_status_threads_connected{`+sel+`})`, "")))
	l.add(6, 4, stat("Queries per second", "ops", q(`sum(rate(mysql_global_status_queries{`+sel+`}[5m]))`, "")))
	l.add(6, 4, stat("Up for", "s", q(`max(mysql_global_status_uptime{`+sel+`})`, "")))
	l.add(12, 8, timeseries("Queries per second, by command", "ops",
		q(`topk(8, sum by (command) (rate(mysql_global_status_commands_total{`+sel+`}[$__rate_interval])) > 0)`, "{{command}}")))
	l.add(12, 8, timeseries("Threads", "none",
		q(`sum(mysql_global_status_threads_connected{`+sel+`})`, "connected"),
		q(`sum(mysql_global_status_threads_running{`+sel+`})`, "running")))
	l.add(12, 8, timeseries("Slow queries per second", "ops",
		q(`sum(rate(mysql_global_status_slow_queries{`+sel+`}[$__rate_interval]))`, "slow queries")))
	l.add(12, 8, timeseries("InnoDB buffer pool", "bytes",
		q(`sum(mysql_global_status_innodb_buffer_pool_bytes_data{`+sel+`})`, "data"),
		q(`sum(mysql_global_variables_innodb_buffer_pool_size{`+sel+`})`, "size")))
	return board{file: "mysql.json", view: v, board: dashboard(v, []map[string]any{variable("db", "Database service", `label_values(mysql_up, service)`)}, l)}
}

func prometheusBoard() board {
	v := DashboardView{UID: "nanocloud-prometheus", Title: "Prometheus itself", About: "Scrape health, samples per second, storage growth"}
	l := &layout{}
	l.add(6, 4, stat("Targets answering", "none", q(`sum(up)`, "")))
	l.add(6, 4, stat("Targets not answering", "none", q(`count(up == 0) or vector(0)`, ""), 1))
	l.add(6, 4, stat("Series", "none", q(`sum(prometheus_tsdb_head_series)`, "")))
	l.add(6, 4, stat("Stored", "bytes", q(storedQuery, "")))
	l.add(12, 8, timeseries("Targets not answering, by job", "none",
		q(`count by (job) (up == 0)`, "{{job}}")))
	l.add(12, 8, timeseries("Samples per second", "none",
		q(`sum(rate(prometheus_tsdb_head_samples_appended_total[$__rate_interval]))`, "samples")))
	l.add(12, 8, timeseries("Slowest scrape, by job", "s",
		q(`max by (job) (scrape_duration_seconds)`, "{{job}}")))
	l.add(12, 8, timeseries("Stored on disk", "bytes",
		q(storedQuery, "stored")))
	return board{file: "prometheus.json", view: v, board: dashboard(v, nil, l)}
}
