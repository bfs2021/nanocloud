package api

import "time"

// RollbackCreate exposes rollbackCreate to the api_test package.
func (s *Server) RollbackCreate(name string, files ...string) { s.rollbackCreate(name, files...) }

// SetBodyTimeout shortens bodyTimeout and returns the function that puts it back.
func SetBodyTimeout(d time.Duration) func() {
	old := bodyTimeout
	bodyTimeout = d
	return func() { bodyTimeout = old }
}
