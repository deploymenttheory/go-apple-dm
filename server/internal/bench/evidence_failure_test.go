package bench

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestScenariosRejectIncompleteEvidence(t *testing.T) {
	cases := []struct {
		id, method, suffix, body string
		occurrence               int
	}{
		{"E2E-001", "GET", "/admin/v1/enrollments/device/", "{}", 1},
		{"E2E-002", "POST", "/commands", `{"Queued":0}`, 1},
		{"E2E-002", "GET", "/result", `{"Status":"Acknowledged"}`, 1},
		{"E2E-004", "GET", "/result", `{"Status":"Error","ErrorChain":[]}`, 1},
		{"E2E-006", "POST", "/push", `{"Sent":false}`, 1},
		{"E2E-007", "POST", "/push", `{"Outcome":"sent"}`, 1},
		{"E2E-008", "GET", "/status", `[]`, 1},
		{"E2E-011", "GET", "/devices", `{"Items":[]}`, 1},
		{"E2E-021", "GET", "/axm/servers", `{"Items":[]}`, 1},
		{"E2E-021", "GET", "/axm/devices", `{"Items":[]}`, 1},
		{"E2E-021", "POST", "/axm/assign", `{}`, 1},
		{"E2E-024", "GET", "/routes", `{"Routes":[]}`, 1},
		{"E2E-024", "GET", "/routes", `{"Routes":[{"Pattern":"GET /routes"}]}`, 1},
		{"APP-001", "POST", "/apppush/send", `{"Accepted":false}`, 1},
		{"APP-003", "GET", "/apppush/credentials", `{"Items":[{"Version":0}]}`, 2},
		{"E2E-013", "POST", "/commands", `{"Queued":0}`, 1},
		{"E2E-013", "POST", "/commands", `{"Queued":1}`, 2},
		{"E2E-020", "POST", "/commands", `{"Queued":0}`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.id+"_"+tc.suffix+"_"+tc.body, func(t *testing.T) {
			w := testWorkspace(t, "simulated")
			selected, err := Select(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			s := selected[0]
			w.Settings = s.Settings
			e, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			base := e.Client.Transport
			count := 0
			e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				match := strings.HasSuffix(r.URL.Path, tc.suffix)
				if strings.HasSuffix(tc.suffix, "/") {
					match = strings.HasPrefix(r.URL.Path, tc.suffix)
				}
				if r.Method == tc.method && match {
					count++
					if count == tc.occurrence {
						return &http.Response{
							StatusCode: 200,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(tc.body)),
							Request:    r,
						}, nil
					}
				}
				return base.RoundTrip(r)
			})
			if err := s.Run(t.Context(), e, ""); err == nil {
				t.Fatal("incomplete evidence passed")
			}
			if count < tc.occurrence {
				t.Fatal("evidence assertion was not reached")
			}
			e.Client.Transport = base
		})
	}
}

func TestNotNowInterruptions(t *testing.T) {
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	base := e.Client.Transport
	// Profile, SCEP, check-in, queue, first delivery, and immediate retry are
	// independently required. Cancellation during the backoff must fail too.
	for _, at := range []int{1, 6, 7, 9} {
		fault := &scenarioFault{base: base, at: at}
		e.Client.Transport = fault
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		err := notNow(ctx, e, "")
		cancel()
		if err == nil {
			t.Fatal("interrupted NotNow passed")
		}
	}
	e.Client.Transport = base
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := notNow(ctx, e, ""); err == nil {
		t.Fatal("cancelled backoff passed")
	}
}
