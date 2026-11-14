package system

// CPUTicks holds the raw tick counters from one /proc/stat sample. Callers
// keep the previous sample and derive CPU usage from the delta. It has no
// build tag so every platform shares one definition.
type CPUTicks struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// Total returns the sum of all tick fields.
func (t CPUTicks) Total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// Busy returns ticks spent doing work (total minus idle).
func (t CPUTicks) Busy() uint64 {
	return t.Total() - t.Idle - t.IOWait
}
