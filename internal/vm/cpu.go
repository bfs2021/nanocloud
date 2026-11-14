package vm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// ErrInvalidCPUShape means a spec describes a CPU no host could present: an
// unknown mode, a topology that does not multiply out to the vCPU count, or a
// cpuset naming CPUs that do not exist. Catching these here keeps the error
// about what was typed rather than libvirt's define-time message.
var ErrInvalidCPUShape = errors.New("vm: invalid CPU shape")

// ValidateCPU refuses a CPU shape that would not define. A hostCPUs of zero
// means the count is unknown: the cpuset's syntax is still checked but not its
// range, so an unreachable libvirtd does not fail every pinned VM.
func ValidateCPU(e metadata.VMEntry, hostCPUs int) error {
	switch e.CPUMode {
	case "", metadata.CPUHostModel, metadata.CPUHostPassthrough:
	default:
		return fmt.Errorf("vm: %q: %w: cpu_mode %q is not %s or %s",
			e.ID, ErrInvalidCPUShape, e.CPUMode, metadata.CPUHostModel, metadata.CPUHostPassthrough)
	}

	if t := e.CPUTopology; t != nil {
		if t.Sockets < 1 || t.Cores < 1 || t.Threads < 1 {
			return fmt.Errorf("vm: %q: %w: topology %d×%d×%d must have at least one socket, core and thread",
				e.ID, ErrInvalidCPUShape, t.Sockets, t.Cores, t.Threads)
		}
		if got := t.Sockets * t.Cores * t.Threads; got != e.VCPUs {
			return fmt.Errorf("vm: %q: %w: topology %d sockets × %d cores × %d threads is %d vCPUs, but the VM has %d",
				e.ID, ErrInvalidCPUShape, t.Sockets, t.Cores, t.Threads, got, e.VCPUs)
		}
	}

	if set := strings.TrimSpace(e.CPUSet); set != "" {
		if _, err := parseCPUSet(set, hostCPUs); err != nil {
			return fmt.Errorf("vm: %q: %w", e.ID, err)
		}
	}
	return nil
}

// parseCPUSet expands libvirt's cpuset syntax (CPU numbers, N-M ranges and ^
// exclusions) into the sorted CPUs it names. "0-7,^4" is seven CPUs.
func parseCPUSet(spec string, hostCPUs int) ([]int, error) {
	include, exclude := map[int]bool{}, map[int]bool{}

	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		target := include
		if strings.HasPrefix(tok, "^") {
			tok, target = strings.TrimSpace(tok[1:]), exclude
		}
		lo, hi, err := parseCPURange(tok)
		if err != nil {
			return nil, err
		}
		if hostCPUs > 0 && hi >= hostCPUs {
			return nil, fmt.Errorf("%w: cpu %d does not exist — this host has %d CPUs (0-%d)",
				ErrInvalidCPUShape, hi, hostCPUs, hostCPUs-1)
		}
		for cpu := lo; cpu <= hi; cpu++ {
			target[cpu] = true
		}
	}

	cpus := make([]int, 0, len(include))
	for cpu := range include {
		if !exclude[cpu] {
			cpus = append(cpus, cpu)
		}
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("%w: %q leaves no CPUs for the VM to run on", ErrInvalidCPUShape, spec)
	}
	sort.Ints(cpus)
	return cpus, nil
}

// parseCPURange reads one "N" or "N-M" element of a cpuset.
func parseCPURange(tok string) (lo, hi int, err error) {
	if tok == "" {
		return 0, 0, fmt.Errorf("%w: empty element — cpu_set looks like \"0-3\", \"0,2,4\" or \"0-7,^4\"", ErrInvalidCPUShape)
	}

	loStr, hiStr, isRange := strings.Cut(tok, "-")
	lo, err = strconv.Atoi(strings.TrimSpace(loStr))
	if err != nil || lo < 0 {
		return 0, 0, fmt.Errorf("%w: %q is not a CPU number", ErrInvalidCPUShape, tok)
	}
	if !isRange {
		return lo, lo, nil
	}

	hi, err = strconv.Atoi(strings.TrimSpace(hiStr))
	if err != nil || hi < lo {
		return 0, 0, fmt.Errorf("%w: %q is not a CPU range — the second number must be the larger one", ErrInvalidCPUShape, tok)
	}
	return lo, hi, nil
}

// CanonicalCPUSet rewrites a cpuset into a single spelling of the set it names.
// libvirt echoes "0,1,2,3" back as "0-3", so both sides of a drift comparison
// go through this. A cpuset that does not parse is returned trimmed, so it
// still compares equal to itself.
func CanonicalCPUSet(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	cpus, err := parseCPUSet(spec, 0)
	if err != nil {
		return spec
	}

	var b strings.Builder
	for i := 0; i < len(cpus); {
		j := i
		for j+1 < len(cpus) && cpus[j+1] == cpus[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(cpus[i]))
		if j > i {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(cpus[j]))
		}
		i = j + 1
	}
	return b.String()
}

// CPUShape is the comparable part of a machine's CPU, used to diff the spec
// against the running domain.
type CPUShape struct {
	// Passthrough records the mode as yes/no: libvirt resolves host-model to a
	// concrete model when the domain starts, so live XML rarely says "host-model".
	Passthrough bool
	// Sockets, Cores and Threads are zero when no topology was fixed.
	Sockets, Cores, Threads int
	// CPUSet is in CanonicalCPUSet form, empty when the VM is unpinned.
	CPUSet string
}

// CPUShapeOf reads the shape a spec asks for.
func CPUShapeOf(e metadata.VMEntry) CPUShape {
	s := CPUShape{
		Passthrough: e.CPUMode == metadata.CPUHostPassthrough,
		CPUSet:      CanonicalCPUSet(e.CPUSet),
	}
	if t := e.CPUTopology; t != nil {
		s.Sockets, s.Cores, s.Threads = t.Sockets, t.Cores, t.Threads
	}
	return s
}

// ParseDomainCPUShape reads the shape a domain has from libvirt's XML.
func ParseDomainCPUShape(xmlStr string) (CPUShape, error) {
	var doc struct {
		VCPU struct {
			CPUSet string `xml:"cpuset,attr"`
		} `xml:"vcpu"`
		CPUTune struct {
			VCPUPin []struct {
				CPUSet string `xml:"cpuset,attr"`
			} `xml:"vcpupin"`
		} `xml:"cputune"`
		CPU struct {
			Mode     string `xml:"mode,attr"`
			Topology struct {
				Sockets int `xml:"sockets,attr"`
				Cores   int `xml:"cores,attr"`
				Threads int `xml:"threads,attr"`
			} `xml:"topology"`
		} `xml:"cpu"`
	}
	if err := xml.Unmarshal([]byte(xmlStr), &doc); err != nil {
		return CPUShape{}, fmt.Errorf("vm: parse domain CPU shape: %w", err)
	}

	// libvirt may report pinning as the <vcpu cpuset> it was given or as per-vCPU
	// <cputune> entries, depending on version and run state. Reading only the
	// first would make a pinned VM look unpinned.
	pinned := doc.VCPU.CPUSet
	if strings.TrimSpace(pinned) == "" && len(doc.CPUTune.VCPUPin) > 0 {
		parts := make([]string, 0, len(doc.CPUTune.VCPUPin))
		for _, p := range doc.CPUTune.VCPUPin {
			if s := strings.TrimSpace(p.CPUSet); s != "" {
				parts = append(parts, s)
			}
		}
		pinned = strings.Join(parts, ",")
	}

	return CPUShape{
		Passthrough: doc.CPU.Mode == metadata.CPUHostPassthrough,
		Sockets:     doc.CPU.Topology.Sockets,
		Cores:       doc.CPU.Topology.Cores,
		Threads:     doc.CPU.Topology.Threads,
		CPUSet:      CanonicalCPUSet(pinned),
	}, nil
}

// CPUShapeDrifted reports whether the running machine's CPU differs from the
// spec. A field the spec leaves unset is not compared, because libvirt may name
// the layout it derived. The cost is a missed restart badge when a topology or
// pinning is removed.
func CPUShapeDrifted(spec, live CPUShape) bool {
	switch {
	case spec.Passthrough != live.Passthrough:
		return true
	case spec.Sockets > 0 && (spec.Sockets != live.Sockets || spec.Cores != live.Cores || spec.Threads != live.Threads):
		return true
	case spec.CPUSet != "" && spec.CPUSet != live.CPUSet:
		return true
	}
	return false
}
