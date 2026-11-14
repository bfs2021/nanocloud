// Package services runs managed services, such as a Kubernetes cluster, a
// PostgreSQL database or S3 storage, on nano-cloud's own VMs.
//
// A service is a recipe (catalog/*.yaml) turned into VMs. The manager pulls the
// recipe's image, creates the VMs through POST /vms, then follows the install
// script over SSH until the service answers on its ports. Each step is saved to
// services.json before the next begins, so a restart resumes where it left off.
// Recipes are cloud-init plus a shell script, so they run on any hypervisor.
package services

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed catalog/*.yaml
var catalogFS embed.FS

// Recipe describes one kind of service.
type Recipe struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Category    string `yaml:"category" json:"category"`
	Description string `yaml:"description" json:"description"`
	// Image is an image family ("ubuntu-24.04"), resolved against the host's
	// catalog at create time so one recipe serves amd64 and arm64.
	Image  string  `yaml:"image" json:"image"`
	Sizes  []Size  `yaml:"sizes" json:"sizes"`
	Params []Param `yaml:"params" json:"params"`
	// Ports are the TCP ports the service answers on. It is ready only once
	// each accepts a connection from the host.
	Ports []int `yaml:"ports" json:"ports"`
	// Install is the setup script body, run once as root on first boot. It is
	// a text/template over the params and the service's name.
	Install string `yaml:"install" json:"-"`
	// Roles describe a service made of several kinds of machine. A recipe has
	// either Roles or Install and Ports, which is shorthand for one role.
	Roles []Role `yaml:"roles" json:"roles,omitempty"`
	// Console enables recipe-specific console features, e.g. "kubernetes".
	Console string `yaml:"console" json:"console,omitempty"`
	// Metrics are exporters set up on the service's VMs once a monitoring
	// service watches it, in addition to the node exporter.
	Metrics []Metric `yaml:"metrics" json:"-"`
	// ReadyCheck runs on the first VM once every install has finished; the
	// service is ready when it exits 0.
	ReadyCheck string `yaml:"ready_check" json:"-"`
	// Endpoints are what a person connects to through the gateway. Templates
	// name each {{.addr_<id>}}, {{.host_<id>}} and {{.port_<id>}}.
	Endpoints []Endpoint `yaml:"endpoints" json:"endpoints"`
	Outputs   []Output   `yaml:"outputs" json:"outputs"`
	Files     []File     `yaml:"files" json:"files"`
}

// Metric names an exporter (see exporters in monitor.go) and the role whose
// VMs it goes on: every VM's when Role is empty.
type Metric struct {
	Exporter string `yaml:"exporter"`
	Role     string `yaml:"role"`
}

// Endpoint is one port of a service that people connect to.
type Endpoint struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
	// Role is the role whose first VM answers on Port; empty without roles.
	Role string `yaml:"role" json:"role,omitempty"`
	Port int    `yaml:"port" json:"port"`
	// Scheme is "http" or "https" for browser endpoints, empty otherwise.
	Scheme string `yaml:"scheme" json:"scheme,omitempty"`
	// anyNode marks a published NodePort, which every node answers on.
	anyNode bool
}

// role resolves an empty Role to mainRole.
func (e Endpoint) role() string {
	if e.Role == "" {
		return mainRole
	}
	return e.Role
}

// scalableRole is the role whose machine count a person chooses and can
// change later, such as a cluster's workers.
func (r Recipe) scalableRole() (Role, bool) {
	for _, role := range r.Roles {
		if role.CountParam != "" {
			return role, true
		}
	}
	return Role{}, false
}

func (r Recipe) param(id string) (Param, bool) {
	for _, p := range r.Params {
		if p.ID == id {
			return p, true
		}
	}
	return Param{}, false
}

// endpointNames are the template names an endpoint adds. Only outputs and
// files may use them, because their values depend on the viewer.
func endpointNames(id string) []string {
	return []string{"addr_" + id, "host_" + id, "port_" + id}
}

// Role is one kind of machine in a service.
type Role struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
	// Count is how many VMs take this role; CountParam instead names a param
	// whose value is the count, so a person can choose it.
	Count      int    `yaml:"count" json:"count,omitempty"`
	CountParam string `yaml:"count_param" json:"count_param,omitempty"`
	// After names an earlier role. This role's install waits until that role
	// is ready and the daemon has handed it Env, which is how a worker learns
	// its control plane's address.
	After string            `yaml:"after" json:"after,omitempty"`
	Env   map[string]string `yaml:"env" json:"-"`
	// Install and Ports are as on a Recipe, for this role's VMs.
	Install string `yaml:"install" json:"-"`
	Ports   []int  `yaml:"ports" json:"ports,omitempty"`
}

// mainRole is the ID of the one role a recipe without Roles has.
const mainRole = "main"

// roles returns the recipe's roles, including the implicit main role.
func (r Recipe) roles() []Role {
	if len(r.Roles) > 0 {
		return r.Roles
	}
	return []Role{{ID: mainRole, Label: r.Name, Count: 1, Install: r.Install, Ports: r.Ports}}
}

func (r Recipe) role(id string) (Role, bool) {
	for _, role := range r.roles() {
		if role.ID == id {
			return role, true
		}
	}
	return Role{}, false
}

// Size is one machine shape a recipe offers.
type Size struct {
	ID       string `yaml:"id" json:"id"`
	Label    string `yaml:"label" json:"label"`
	VCPUs    int    `yaml:"vcpus" json:"vcpus"`
	MemoryMB int    `yaml:"memory_mb" json:"memory_mb"`
	DiskGB   int    `yaml:"disk_gb" json:"disk_gb"`
	Default  bool   `yaml:"default" json:"default,omitempty"`
}

// Param is one template value: either a default a person may change,
// checked against Pattern, or Generate random characters from Charset.
type Param struct {
	ID       string `yaml:"id" json:"id"`
	Label    string `yaml:"label" json:"label"`
	Default  string `yaml:"default" json:"default,omitempty"`
	Pattern  string `yaml:"pattern" json:"pattern,omitempty"`
	Hint     string `yaml:"hint" json:"hint,omitempty"`
	Generate int    `yaml:"generate" json:"generate,omitempty"`
	// Charset is "alnum" (the default) or "upper", the shape of an S3 key.
	Charset string `yaml:"charset" json:"-"`
	Secret  bool   `yaml:"secret" json:"secret,omitempty"`
}

// Output is one value a ready service shows its user, such as a connection
// string. Value is a template over the params, name and addresses.
type Output struct {
	ID     string `yaml:"id" json:"id"`
	Label  string `yaml:"label" json:"label"`
	Value  string `yaml:"value" json:"-"`
	Secret bool   `yaml:"secret" json:"secret,omitempty"`
}

// File is a file a ready service offers for download, read from the VM over
// SSH. Replace rewrites it on the way out, e.g. a kubeconfig's 127.0.0.1.
type File struct {
	ID       string            `yaml:"id" json:"id"`
	Label    string            `yaml:"label" json:"label"`
	Filename string            `yaml:"filename" json:"-"`
	Path     string            `yaml:"path" json:"-"`
	Replace  map[string]string `yaml:"replace" json:"-"`
}

// DefaultSize returns the recipe's default size, or its first.
func (r Recipe) DefaultSize() Size {
	for _, s := range r.Sizes {
		if s.Default {
			return s
		}
	}
	return r.Sizes[0]
}

// Size returns the size with the given ID; an empty ID means the default.
func (r Recipe) Size(id string) (Size, bool) {
	if id == "" {
		return r.DefaultSize(), true
	}
	for _, s := range r.Sizes {
		if s.ID == id {
			return s, true
		}
	}
	return Size{}, false
}

// imageFamilies maps a recipe's image family to its catalog IDs, one per
// architecture. The host's catalog lists only its own architecture.
var imageFamilies = map[string][]string{
	"ubuntu-24.04": {"ubuntu-2404-standard", "ubuntu-2404-standard-arm64"},
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var catalog = mustLoadCatalog(catalogFS)

// Catalog returns every recipe, ordered by name.
func Catalog() []Recipe {
	out := make([]Recipe, len(catalog))
	copy(out, catalog)
	return out
}

// RecipeByID returns the recipe with the given ID.
func RecipeByID(id string) (Recipe, bool) {
	for _, r := range catalog {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

func mustLoadCatalog(fsys fs.FS) []Recipe {
	recipes, err := loadCatalog(fsys)
	if err != nil {
		panic(err)
	}
	return recipes
}

// loadCatalog parses and checks every recipe. A broken recipe is a bug in
// this binary, caught by the tests, so the embedded catalog panics on it.
func loadCatalog(fsys fs.FS) ([]Recipe, error) {
	names, err := fs.Glob(fsys, "catalog/*.yaml")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var recipes []Recipe
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var r Recipe
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("services: %s: %w", path.Base(name), err)
		}
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("services: %s: %w", path.Base(name), err)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("services: %s: recipe id %q is used twice", path.Base(name), r.ID)
		}
		seen[r.ID] = true
		recipes = append(recipes, r.withEmptyLists())
	}
	sort.Slice(recipes, func(i, j int) bool { return recipes[i].Name < recipes[j].Name })
	return recipes, nil
}

// withEmptyLists replaces nil lists with empty ones so the JSON has [], not
// null.
func (r Recipe) withEmptyLists() Recipe {
	if r.Params == nil {
		r.Params = []Param{}
	}
	if r.Ports == nil {
		r.Ports = []int{}
	}
	if r.Endpoints == nil {
		r.Endpoints = []Endpoint{}
	}
	if r.Outputs == nil {
		r.Outputs = []Output{}
	}
	if r.Files == nil {
		r.Files = []File{}
	}
	return r
}

func (r Recipe) validate() error {
	switch {
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("id %q must be lowercase letters, digits and _", r.ID)
	case r.Name == "" || r.Description == "":
		return fmt.Errorf("needs a name and a description")
	case imageFamilies[r.Image] == nil:
		return fmt.Errorf("unknown image family %q", r.Image)
	case len(r.Sizes) == 0:
		return fmt.Errorf("needs at least one size")
	case len(r.Roles) == 0 && strings.TrimSpace(r.Install) == "":
		return fmt.Errorf("needs an install script or roles")
	case len(r.Roles) > 0 && (r.Install != "" || len(r.Ports) > 0):
		return fmt.Errorf("a recipe with roles puts its install and ports on them")
	case r.Console != "" && r.Console != consoleKubernetes && r.Console != consoleMonitoring:
		return fmt.Errorf("unknown console %q", r.Console)
	}
	for _, mt := range r.Metrics {
		if _, ok := exporters[mt.Exporter]; !ok || mt.Exporter == nodeExporter {
			return fmt.Errorf("unknown metrics exporter %q", mt.Exporter)
		}
		if _, ok := r.role(mt.Role); mt.Role != "" && !ok {
			return fmt.Errorf("metrics %q are on role %q, which the recipe does not have", mt.Exporter, mt.Role)
		}
	}
	defaults := 0
	for _, s := range r.Sizes {
		if s.ID == "" || s.VCPUs <= 0 || s.MemoryMB <= 0 || s.DiskGB <= 0 {
			return fmt.Errorf("size %q needs an id and positive vcpus, memory_mb and disk_gb", s.ID)
		}
		if s.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return fmt.Errorf("more than one default size")
	}

	// Every name a template may use: the params, plus the service's name,
	// machine count and VM addresses ({{.ip}}, {{.<role>_ip}}).
	names := map[string]bool{"name": true, "nodes": true, "ip": true}
	for _, role := range r.Roles {
		names[role.ID+"_ip"] = true
	}
	viewOnly := map[string]bool{}
	for _, e := range r.Endpoints {
		for _, n := range endpointNames(e.ID) {
			viewOnly[n] = true
			names[n] = true
		}
	}
	params := map[string]Param{}
	for _, p := range r.Params {
		params[p.ID] = p
		if !idPattern.MatchString(p.ID) || names[p.ID] {
			return fmt.Errorf("param id %q is invalid or used twice", p.ID)
		}
		names[p.ID] = true
		switch {
		case p.Generate < 0:
			return fmt.Errorf("param %q: generate must be a length", p.ID)
		case p.Generate > 0 && (p.Default != "" || p.Pattern != ""):
			return fmt.Errorf("param %q: a generated param takes no default or pattern", p.ID)
		case p.Generate == 0 && p.Default == "":
			return fmt.Errorf("param %q needs a default or generate", p.ID)
		case p.Charset != "" && p.Charset != charsetAlnum && p.Charset != charsetUpper:
			return fmt.Errorf("param %q: unknown charset %q", p.ID, p.Charset)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return fmt.Errorf("param %q: %w", p.ID, err)
			}
			if !re.MatchString(p.Default) {
				return fmt.Errorf("param %q: default %q does not match its own pattern", p.ID, p.Default)
			}
		}
	}

	// Render every template now against a full set of values, so a typo like
	// {{.pasword}} fails the tests rather than a user's service.
	sample := map[string]string{}
	for n := range names {
		sample[n] = "x"
	}
	check := func(what, text string) error {
		if _, err := render(text, sample); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	addresses := map[string]string{}
	for n := range sample {
		if n == "ip" || strings.HasSuffix(n, "_ip") {
			addresses[n] = sample[n]
			delete(sample, n)
		}
	}
	for n := range viewOnly {
		delete(sample, n)
	}
	seenRoles := map[string]bool{}
	for _, role := range r.roles() {
		switch {
		case !idPattern.MatchString(role.ID) || seenRoles[role.ID]:
			return fmt.Errorf("role id %q is invalid or used twice", role.ID)
		case strings.TrimSpace(role.Install) == "":
			return fmt.Errorf("role %q needs an install script", role.ID)
		case (role.Count > 0) == (role.CountParam != ""):
			return fmt.Errorf("role %q needs either a count or a count_param", role.ID)
		case role.After != "" && !seenRoles[role.After]:
			return fmt.Errorf("role %q waits for %q, which is not an earlier role", role.ID, role.After)
		case role.After == "" && len(role.Env) > 0:
			return fmt.Errorf("role %q has env but waits for no role to hand it over", role.ID)
		}
		if role.CountParam != "" {
			p, ok := params[role.CountParam]
			if !ok || p.Generate > 0 || p.Pattern == "" {
				return fmt.Errorf("role %q: count_param %q must be a param with a pattern", role.ID, role.CountParam)
			}
		}
		// The install is written before any VM has an address.
		if err := check("role "+role.ID+" install", role.Install); err != nil {
			return err
		}
		// Env is rendered once the role it waits for has its addresses.
		sample[role.After+"_ip"] = "x"
		for k, v := range role.Env {
			if !envKeyPattern.MatchString(k) {
				return fmt.Errorf("role %q: env name %q must be upper-case letters, digits and _", role.ID, k)
			}
			if err := check("role "+role.ID+" env "+k, v); err != nil {
				return err
			}
		}
		delete(sample, role.After+"_ip")
		for _, port := range role.Ports {
			if port <= 0 || port > 65535 {
				return fmt.Errorf("role %q: port %d is out of range", role.ID, port)
			}
		}
		seenRoles[role.ID] = true
	}
	for n, v := range addresses {
		sample[n] = v
	}
	if err := check("ready_check", r.ReadyCheck); err != nil {
		return err
	}
	if err := r.validateEndpoints(); err != nil {
		return err
	}
	for n := range viewOnly {
		sample[n] = "x"
	}
	for _, o := range r.Outputs {
		if o.ID == "" || o.Label == "" {
			return fmt.Errorf("every output needs an id and a label")
		}
		if err := check("output "+o.ID, o.Value); err != nil {
			return err
		}
	}
	for _, f := range r.Files {
		if f.ID == "" || f.Label == "" || f.Filename == "" || !path.IsAbs(f.Path) {
			return fmt.Errorf("file %q needs an id, a label, a filename and an absolute path", f.ID)
		}
		if err := check("file "+f.ID, f.Filename); err != nil {
			return err
		}
		for from, to := range f.Replace {
			if err := check("file "+f.ID+" replace", from+to); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateEndpoints checks that every endpoint is on a role that answers on
// its port, so the service is not ready until the endpoint is.
func (r Recipe) validateEndpoints() error {
	seen := map[string]bool{}
	for _, e := range r.Endpoints {
		if !idPattern.MatchString(e.ID) || seen[e.ID] || e.Label == "" {
			return fmt.Errorf("endpoint %q needs a label and an id of its own", e.ID)
		}
		seen[e.ID] = true
		if len(r.Roles) == 0 && e.Role != "" {
			return fmt.Errorf("endpoint %q: a recipe without roles names none", e.ID)
		}
		role, ok := r.role(e.role())
		if !ok {
			return fmt.Errorf("endpoint %q is on role %q, which the recipe does not have", e.ID, e.role())
		}
		onRole := false
		for _, p := range role.Ports {
			onRole = onRole || p == e.Port
		}
		if !onRole {
			return fmt.Errorf("endpoint %q: port %d is not one of role %q's ports", e.ID, e.Port, role.ID)
		}
		if e.Scheme != "" && e.Scheme != "http" && e.Scheme != "https" {
			return fmt.Errorf("endpoint %q: scheme %q is not http or https", e.ID, e.Scheme)
		}
	}
	return nil
}

var envKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// render executes a recipe template over values. A missing name is an error
// rather than an empty string, which could yield an empty password.
func render(text string, values map[string]string) (string, error) {
	t, err := template.New("").Option("missingkey=error").Funcs(template.FuncMap{"sh": shellQuote}).Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, values); err != nil {
		return "", err
	}
	return b.String(), nil
}

// shellQuote quotes s as one shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
