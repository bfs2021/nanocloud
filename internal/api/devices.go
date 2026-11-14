package api

import "net/http"

// handleGetDevices handles GET /system/devices. Like GET /system/deps it is a
// readiness readout: a host with IOMMU off still answers 200, with the
// kernel-cmdline remedy filled in. Only an unreachable libvirt is a failure.
func (s *Server) handleGetDevices(w http.ResponseWriter, _ *http.Request) {
	report, err := s.deviceReport()
	if err != nil {
		writeVMManagerError(w, "list host devices", err)
		return
	}
	writeJSON(w, report)
}
