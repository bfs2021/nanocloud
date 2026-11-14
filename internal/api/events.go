package api

import (
	"net/http"
	"strconv"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// maxEventsPage bounds ?limit= at the ring's capacity, which also keeps the
// value from wrapping when narrowed to an int.
const maxEventsPage = events.DefaultCapacity

type eventsResponse struct {
	// Events is never null, since the UI maps over it.
	Events []events.Event `json:"events"`
	// Cursor is what the next poll should send as ?since=.
	Cursor uint64 `json:"cursor"`
}

// handleEvents handles GET /events?since={cursor}&limit={n}, oldest first.
// Omitting since reads everything the ring holds. The feed lives in memory
// and starts empty after every restart.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, err := cursorParam(r, "since")
	if err != nil {
		http.Error(w, "since must be a non-negative integer", http.StatusBadRequest)
		return
	}
	limit, err := cursorParam(r, "limit")
	if err != nil {
		http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
		return
	}

	evs, cursor := s.events.Since(since, int(min(limit, maxEventsPage)))
	if evs == nil {
		evs = []events.Event{}
	}
	writeJSON(w, eventsResponse{Events: evs, Cursor: cursor})
}

// cursorParam reads a non-negative integer query parameter, absent meaning
// zero. A malformed value is an error: a silent zero for since would replay
// the whole ring on every poll.
func cursorParam(r *http.Request, name string) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}
