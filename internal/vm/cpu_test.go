package vm

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

func topo(sockets, cores, threads int) *metadata.CPUTopology {
	return &metadata.CPUTopology{Sockets: sockets, Cores: cores, Threads: threads}
}

// TestValidateCPU covers the three ways a CPU shape can be wrong. Each would
// otherwise surface as a libvirt define-time error.
func TestValidateCPU(t *testing.T) {
	const hostCPUs = 8

	tests := []struct {
		name     string
		vcpus    int
		mode     string
		topology *metadata.CPUTopology
		cpuSet   string
		hostCPUs int
		wantErr  bool
	}{
		{name: "the default machine", vcpus: 4, hostCPUs: hostCPUs},
		{name: "host-model named explicitly", vcpus: 4, mode: metadata.CPUHostModel, hostCPUs: hostCPUs},
		{name: "host-passthrough", vcpus: 4, mode: metadata.CPUHostPassthrough, hostCPUs: hostCPUs},
		{name: "an invented mode", vcpus: 4, mode: "host-model-ish", hostCPUs: hostCPUs, wantErr: true},

		{name: "topology multiplying out to the vcpu count", vcpus: 4, topology: topo(1, 2, 2), hostCPUs: hostCPUs},
		{name: "one socket per vcpu", vcpus: 4, topology: topo(4, 1, 1), hostCPUs: hostCPUs},
		{name: "topology short of the vcpu count", vcpus: 4, topology: topo(1, 2, 1), hostCPUs: hostCPUs, wantErr: true},
		{name: "topology over the vcpu count", vcpus: 4, topology: topo(2, 2, 2), hostCPUs: hostCPUs, wantErr: true},
		{name: "a topology with no threads", vcpus: 4, topology: topo(2, 2, 0), hostCPUs: hostCPUs, wantErr: true},

		{name: "a pinned range", vcpus: 2, cpuSet: "4-7", hostCPUs: hostCPUs},
		{name: "a pinned list", vcpus: 2, cpuSet: "0,2,4", hostCPUs: hostCPUs},
		{name: "a range with an exclusion", vcpus: 2, cpuSet: "0-7,^4", hostCPUs: hostCPUs},
		{name: "spaces around the elements", vcpus: 2, cpuSet: " 0 , 2 ", hostCPUs: hostCPUs},
		{name: "a cpu this host does not have", vcpus: 2, cpuSet: "0-15", hostCPUs: hostCPUs, wantErr: true},
		{name: "the first cpu this host does not have", vcpus: 2, cpuSet: "8", hostCPUs: hostCPUs, wantErr: true},
		// Excluding everything leaves nowhere to run; libvirt reports a bitmap error.
		{name: "an exclusion that empties the set", vcpus: 2, cpuSet: "0-3,^0-3", hostCPUs: hostCPUs, wantErr: true},
		{name: "a backwards range", vcpus: 2, cpuSet: "7-4", hostCPUs: hostCPUs, wantErr: true},
		{name: "a negative cpu", vcpus: 2, cpuSet: "-1", hostCPUs: hostCPUs, wantErr: true},
		{name: "not a number at all", vcpus: 2, cpuSet: "all", hostCPUs: hostCPUs, wantErr: true},
		{name: "a trailing comma", vcpus: 2, cpuSet: "0,", hostCPUs: hostCPUs, wantErr: true},
		// With an unknown host CPU count only the syntax is checked, so a briefly
		// unreachable libvirtd does not fail validation.
		{name: "range check skipped when the host count is unknown", vcpus: 2, cpuSet: "0-63", hostCPUs: 0},
		{name: "syntax still checked when the host count is unknown", vcpus: 2, cpuSet: "nope", hostCPUs: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := metadata.VMEntry{ID: "vm", VCPUs: tt.vcpus, CPUMode: tt.mode, CPUTopology: tt.topology, CPUSet: tt.cpuSet}

			err := ValidateCPU(e, tt.hostCPUs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCPU = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidCPUShape) {
				t.Errorf("error does not wrap ErrInvalidCPUShape, so callers cannot classify it: %v", err)
			}
		})
	}
}

// cpuFacts is the rendered CPU shape, read back schema-free so a misspelt
// attribute cannot pass by symmetry.
type cpuFacts struct {
	Mode      string
	Topology  string // "sockets×cores×threads", empty when absent
	Placement string
	CPUSet    string
	VCPUText  string
}

func cpuFactsOf(root xnode) cpuFacts {
	f := cpuFacts{}
	if c := root.find("cpu"); len(c) > 0 {
		f.Mode = c[0].attr("mode")
		if tp := c[0].find("topology"); len(tp) > 0 {
			f.Topology = tp[0].attr("sockets") + "×" + tp[0].attr("cores") + "×" + tp[0].attr("threads")
		}
	}
	if v := root.find("vcpu"); len(v) > 0 {
		f.Placement, f.CPUSet, f.VCPUText = v[0].attr("placement"), v[0].attr("cpuset"), strings.TrimSpace(v[0].Text)
	}
	return f
}

// TestBuildDomainXML_CPUShape pins where each field lands: the topology inside
// <cpu>, the pinning on <vcpu>.
func TestBuildDomainXML_CPUShape(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		topology *metadata.CPUTopology
		cpuSet   string
		want     cpuFacts
	}{
		{
			// What older VMs and clients that never send `cpu` get.
			name: "the default is host-model, underived and unpinned",
			want: cpuFacts{Mode: "host-model", VCPUText: "2"},
		},
		{
			name: "host-passthrough is the only mode that changes the attribute",
			mode: metadata.CPUHostPassthrough,
			want: cpuFacts{Mode: "host-passthrough", VCPUText: "2"},
		},
		{
			// No write path produces this; rendering it would make libvirt refuse the define.
			name: "an unknown mode falls back to the default",
			mode: "banana",
			want: cpuFacts{Mode: "host-model", VCPUText: "2"},
		},
		{
			name:     "a topology becomes a child of cpu",
			topology: topo(1, 1, 2),
			want:     cpuFacts{Mode: "host-model", Topology: "1×1×2", VCPUText: "2"},
		},
		{
			// libvirt infers placement='static' but virsh writes it, so match a hand edit.
			name:   "a cpuset pins the vcpu element",
			cpuSet: "4-7",
			want:   cpuFacts{Mode: "host-model", Placement: "static", CPUSet: "4-7", VCPUText: "2"},
		},
		{
			name:     "all three at once",
			mode:     metadata.CPUHostPassthrough,
			topology: topo(2, 1, 1),
			cpuSet:   "0,2",
			want:     cpuFacts{Mode: "host-passthrough", Topology: "2×1×1", Placement: "static", CPUSet: "0,2", VCPUText: "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := baseEntry()
			e.CPUMode, e.CPUTopology, e.CPUSet = tt.mode, tt.topology, tt.cpuSet

			got := cpuFactsOf(parseDoc(t, DomainSpecFromEntry(e, "/d.qcow2", "/d.iso")))
			if got != tt.want {
				t.Errorf("cpu = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCanonicalCPUSet(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"0-3", "0-3"},
		// libvirt echoes a list back as a range, so both spellings reduce to one.
		{"0,1,2,3", "0-3"},
		{"3,1,0,2", "0-3"},
		{"0,2,4", "0,2,4"},
		{"0-7,^4", "0-3,5-7"},
		{"2,2,2", "2"},
		{" 4 - 5 ", "4-5"},
		// Unparseable, so returned as-is; it still compares equal to itself.
		{"nonsense", "nonsense"},
	}

	for _, tt := range tests {
		if got := CanonicalCPUSet(tt.in); got != tt.want {
			t.Errorf("CanonicalCPUSet(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// runningHostModelXML is a running domain defined with mode='host-model'.
// libvirt resolves host-model at start, so the live XML says custom, which is
// why CPUShape records passthrough-ness rather than the mode. It is
// hand-written from libvirt's documented format, not captured from a host.
const runningHostModelXML = `<domain type='kvm'>
  <name>web</name>
  <vcpu placement='static'>4</vcpu>
  <cputune>
    <vcpupin vcpu='0' cpuset='4-5'/>
    <vcpupin vcpu='1' cpuset='6-7'/>
  </cputune>
  <cpu mode='custom' match='exact' check='full'>
    <model fallback='forbid'>Skylake-Client-IBRS</model>
    <vendor>Intel</vendor>
    <topology sockets='1' dies='1' clusters='1' cores='2' threads='2'/>
    <feature policy='require' name='hypervisor'/>
  </cpu>
</domain>`

const runningPassthroughXML = `<domain type='kvm'>
  <name>gpu</name>
  <vcpu placement='static' cpuset='0-3'>4</vcpu>
  <cpu mode='host-passthrough' check='none' migratable='on'/>
</domain>`

func TestParseDomainCPUShape(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want CPUShape
	}{
		{
			// libvirt writes dies and clusters, which we never do; they must be ignored.
			name: "a resolved host-model domain is not passthrough",
			xml:  runningHostModelXML,
			want: CPUShape{Sockets: 1, Cores: 2, Threads: 2, CPUSet: "4-7"},
		},
		{
			name: "passthrough survives the round trip",
			xml:  runningPassthroughXML,
			want: CPUShape{Passthrough: true, CPUSet: "0-3"},
		},
		{
			name: "a domain with no cpu element at all",
			xml:  `<domain type='kvm'><name>x</name><vcpu>1</vcpu></domain>`,
			want: CPUShape{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDomainCPUShape(tt.xml)
			if err != nil {
				t.Fatalf("ParseDomainCPUShape: %v", err)
			}
			if got != tt.want {
				t.Errorf("shape = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestCPUShapeDrifted guards the "restart to apply" badge. Its worst failure is
// being on for a VM nobody touched.
func TestCPUShapeDrifted(t *testing.T) {
	tests := []struct {
		name       string
		spec, live CPUShape
		want       bool
	}{
		{
			// A running host-model domain reports the concrete model libvirt picked.
			name: "a host-model spec against the model libvirt resolved it to",
			spec: CPUShape{},
			live: CPUShape{Sockets: 1, Cores: 4, Threads: 1},
		},
		{
			// No topology asked for means whatever libvirt derives, so a derived one in
			// the live XML is agreement.
			name: "no topology asked for, one reported",
			spec: CPUShape{},
			live: CPUShape{Sockets: 4, Cores: 1, Threads: 1},
		},
		{
			name: "the same pinning spelled two ways",
			spec: CPUShape{CPUSet: CanonicalCPUSet("0,1,2,3")},
			live: CPUShape{CPUSet: CanonicalCPUSet("0-3")},
		},
		{name: "mode changed", spec: CPUShape{Passthrough: true}, live: CPUShape{}, want: true},
		{
			name: "topology changed",
			spec: CPUShape{Sockets: 2, Cores: 1, Threads: 1},
			live: CPUShape{Sockets: 1, Cores: 2, Threads: 1},
			want: true,
		},
		{
			name: "a topology asked for that the machine does not have",
			spec: CPUShape{Sockets: 1, Cores: 2, Threads: 2},
			live: CPUShape{},
			want: true,
		},
		{name: "pinning changed", spec: CPUShape{CPUSet: "0-3"}, live: CPUShape{CPUSet: "4-7"}, want: true},
		{name: "pinning added", spec: CPUShape{CPUSet: "0-3"}, live: CPUShape{}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CPUShapeDrifted(tt.spec, tt.live); got != tt.want {
				t.Errorf("CPUShapeDrifted(%+v, %+v) = %v, want %v", tt.spec, tt.live, got, tt.want)
			}
		})
	}
}

// TestCPUShapeOf pins the one asymmetry between the stored spec and the shape:
// an empty mode is host-model, not "no mode".
func TestCPUShapeOf(t *testing.T) {
	tests := []struct {
		name string
		e    metadata.VMEntry
		want CPUShape
	}{
		{name: "a legacy entry with no cpu keys", e: metadata.VMEntry{}, want: CPUShape{}},
		{name: "host-model spelled out", e: metadata.VMEntry{CPUMode: metadata.CPUHostModel}, want: CPUShape{}},
		{name: "passthrough", e: metadata.VMEntry{CPUMode: metadata.CPUHostPassthrough}, want: CPUShape{Passthrough: true}},
		{
			name: "topology and pinning",
			e:    metadata.VMEntry{CPUTopology: topo(1, 2, 2), CPUSet: "0,1,2,3"},
			want: CPUShape{Sockets: 1, Cores: 2, Threads: 2, CPUSet: "0-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CPUShapeOf(tt.e); got != tt.want {
				t.Errorf("CPUShapeOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestValidateCPU_ReachesTheDefinePaths: a hand-edited metadata.json must not
// get past validation, including on the reconcile loop's DefineFromSpec path.
func TestValidateCPU_ReachesTheDefinePaths(t *testing.T) {
	m := newTestManager(t)

	e := metadata.VMEntry{ID: "bad-cpu", VCPUs: 4, MemoryMB: 1024, CPUTopology: topo(1, 1, 1)}
	err := m.DefineFromSpec(e, "/pool/bad-cpu.qcow2", "/pool/bad-cpu.iso")
	if !errors.Is(err, ErrInvalidCPUShape) {
		t.Fatalf("DefineFromSpec = %v, want ErrInvalidCPUShape", err)
	}

	if err := m.Define(VMCreateParams{
		Name: "bad-cpu", DiskPath: "/pool/bad-cpu.qcow2", CloudInitISO: "/pool/bad-cpu.iso",
		VCPUs: 4, MemoryMB: 1024, CPUMode: "not-a-mode",
	}); !errors.Is(err, ErrInvalidCPUShape) {
		t.Fatalf("Define = %v, want ErrInvalidCPUShape", err)
	}
}

// TestHostCPUs_ReportsTheDriversCount: a zero count silently disables the range check.
func TestHostCPUs_ReportsTheDriversCount(t *testing.T) {
	m := newTestManager(t)

	if got := m.HostCPUs(); got <= 0 {
		t.Fatalf("HostCPUs = %d, want the driver's CPU count — zero disables the cpuset range check", got)
	}
}

// TestParseCPUSet_Expansion covers the exclusion form, which is the only part
// of the syntax a split-on-commas parser would get wrong rather than reject.
func TestParseCPUSet_Expansion(t *testing.T) {
	got, err := parseCPUSet("0-7,^4,^6", 8)
	if err != nil {
		t.Fatalf("parseCPUSet: %v", err)
	}
	if want := []int{0, 1, 2, 3, 5, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseCPUSet = %v, want %v", got, want)
	}
}
