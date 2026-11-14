package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/events"
)

func eventsGet(ring *events.Ring, target string) *httptest.ResponseRecorder {
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}, Events: ring})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func ringWith(n int) *events.Ring {
	r := events.New(events.DefaultCapacity)
	for i := 0; i < n; i++ {
		r.Append(events.Event{Level: events.LevelWarn, Kind: events.KindVMRestarted, VM: "web-01", Message: "restarted web-01"})
	}
	return r
}

func TestHandleEvents(t *testing.T) {
	tests := []struct {
		name       string
		ring       *events.Ring
		target     string
		wantStatus int
		wantSeqs   []uint64
		wantCursor uint64
	}{
		{name: "returns the feed and a cursor", ring: ringWith(3), target: "/events", wantStatus: 200, wantSeqs: []uint64{1, 2, 3}, wantCursor: 3},
		{name: "since resumes after the cursor", ring: ringWith(3), target: "/events?since=2", wantStatus: 200, wantSeqs: []uint64{3}, wantCursor: 3},
		{name: "limit caps the page", ring: ringWith(5), target: "/events?limit=2", wantStatus: 200, wantSeqs: []uint64{1, 2}, wantCursor: 2},
		// Without a ring the feed is empty, not a 503.
		{name: "nil ring reports an empty feed", ring: nil, target: "/events", wantStatus: 200, wantCursor: 0},
		// A malformed cursor read as 0 would replay the whole ring on every poll.
		{name: "rejects non-numeric since", ring: ringWith(1), target: "/events?since=abc", wantStatus: 400},
		{name: "rejects negative since", ring: ringWith(1), target: "/events?since=-1", wantStatus: 400},
		{name: "rejects non-numeric limit", ring: ringWith(1), target: "/events?limit=abc", wantStatus: 400},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := eventsGet(tc.ring, tc.target)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			var body struct {
				Events []events.Event `json:"events"`
				Cursor uint64         `json:"cursor"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if len(body.Events) != len(tc.wantSeqs) {
				t.Fatalf("got %d events, want %d", len(body.Events), len(tc.wantSeqs))
			}
			for i, want := range tc.wantSeqs {
				if body.Events[i].Seq != want {
					t.Errorf("events[%d].seq = %d, want %d", i, body.Events[i].Seq, want)
				}
			}
			if body.Cursor != tc.wantCursor {
				t.Errorf("cursor = %d, want %d", body.Cursor, tc.wantCursor)
			}
		})
	}
}

// The UI maps over "events" directly, so this asserts on the raw body that it
// is never null.
func TestHandleEvents_SerialisesFieldsAndNeverNulls(t *testing.T) {
	body := eventsGet(ringWith(1), "/events").Body.String()

	for _, want := range []string{`"kind":"vm.restarted"`, `"vm":"web-01"`, `"level":"warn"`, `"at":"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s missing %s", body, want)
		}
	}
	if empty := eventsGet(events.New(0), "/events").Body.String(); !strings.Contains(empty, `"events":[]`) {
		t.Errorf("empty feed = %s, want an empty array not null", empty)
	}
}
