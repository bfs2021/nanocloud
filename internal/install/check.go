package install

// Check is the result of a single host-readiness check. A failed required
// check means nano-cloud cannot work; optional checks are informational.
type Check struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Message  string `json:"message"`
	// Remedy is a copy-pasteable shell command that fixes the problem.
	Remedy string `json:"remedy,omitempty"`
}
