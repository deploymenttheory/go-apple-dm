package dmctl_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestManagedContentCLI checks explicit methods, selected IDs, and unchanged
// request objects at the actual CLI HTTP boundary, including sensitive file input.
func TestManagedContentCLI(t *testing.T) {
	recorder, env := mdmServer(t)
	for _, tc := range []struct {
		family, verb string
		ids          []string
		want         string
	}{
		{"appsbooks", "get", []string{"london"}, "GET /admin/v1/appsbooks/locations/london"},
		{"appsbooks", "evidence", []string{"london"}, "GET /admin/v1/appsbooks/locations/london/evidence"},
		{"appsbooks", "register", []string{"london"}, "PUT /admin/v1/appsbooks/locations/london"},
		{"appsbooks", "receiver", []string{"london"}, "PUT /admin/v1/appsbooks/locations/london/receiver"},
		{"appsbooks", "configure", []string{"london"}, "POST /admin/v1/appsbooks/locations/london/configure"},
		{"appsbooks", "refresh", []string{"london"}, "POST /admin/v1/appsbooks/locations/london/refresh"},
		{"appsbooks", "probe", []string{"london"}, "POST /admin/v1/appsbooks/locations/london/probe"},
		{"appsbooks", "recover-configuration", []string{"london"}, "POST /admin/v1/appsbooks/locations/london/recover-configuration"},
		{"appsbooks", "recover-licensing", []string{"london"}, "POST /admin/v1/appsbooks/locations/london/recover-licensing"},
		{"appsbooks", "job-get", []string{"london", "job"}, "GET /admin/v1/appsbooks/locations/london/jobs/job"},
		{"appsbooks", "job-evidence", []string{"london", "job"}, "GET /admin/v1/appsbooks/locations/london/jobs/job/evidence"},
		{"appsbooks", "job-create", []string{"london", "job"}, "PUT /admin/v1/appsbooks/locations/london/jobs/job"},
		{"appsbooks", "submit", []string{"london", "job"}, "POST /admin/v1/appsbooks/locations/london/jobs/job/submit"},
		{"appsbooks", "poll", []string{"london", "job"}, "POST /admin/v1/appsbooks/locations/london/jobs/job/poll"},
		{"appsbooks", "reconcile", []string{"london", "job"}, "POST /admin/v1/appsbooks/locations/london/jobs/job/reconcile"},
		{"managed-content", "native-observation", []string{"chosen"}, "GET /admin/v1/managed-content/operations/chosen/native-observation"},
		{"managed-content", "status-refresh", []string{"chosen"}, "POST /admin/v1/managed-content/operations/chosen/status-refresh"},
		{"managed-content", "get", []string{"chosen"}, "GET /admin/v1/managed-content/operations/chosen"},
		{"managed-content", "evidence", []string{"chosen"}, "GET /admin/v1/managed-content/operations/chosen/evidence"},
		{"managed-content", "create", []string{"chosen"}, "PUT /admin/v1/managed-content/operations/chosen"},
		{"managed-content", "step", []string{"chosen"}, "POST /admin/v1/managed-content/operations/chosen/step"},
		{"managed-content", "reconcile", []string{"chosen"}, "POST /admin/v1/managed-content/operations/chosen/reconcile"},
		{"managed-content", "cancel", []string{"chosen"}, "POST /admin/v1/managed-content/operations/chosen/cancel"},
	} {
		t.Run(tc.family+"/"+tc.verb, func(t *testing.T) {
			args := append([]string{tc.family, tc.verb}, tc.ids...)
			if _, _, err := runWithStdin(t, env, `{"worker":"operator","leaseSeconds":60}`, args...); err != nil {
				t.Fatal(err)
			}
			seen := recorder.seen()
			if got := seen[len(seen)-1]; got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}

// TestManagedContentCLIRejectsAmbiguousInput ensures malformed or unselected
// operations never contact the server and credentials stay absent from errors.
func TestManagedContentCLIRejectsAmbiguousInput(t *testing.T) {
	rec, env := mdmServer(t)
	for _, args := range [][]string{{"appsbooks"}, {"managed-content"}, {"appsbooks", "unknown", "id"}, {"managed-content", "unknown", "id"}, {"appsbooks", "get"}, {"managed-content", "get", "id", "other"}, {"appsbooks", "submit", "location"}, {"appsbooks", "get", "one", "two"}, {"appsbooks", "get", "-unknown"}} {
		if _, _, err := run(t, env, args...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal(args, err)
		}
	}
	for _, body := range []string{`{`, `[]`, `null`, `{"token":"secret"} {}`} {
		_, stderr, err := runWithStdin(t, env, body, "appsbooks", "receiver", "location")
		if !errors.Is(err, dmctl.ErrUsage) || strings.Contains(stderr, "secret") {
			t.Fatal(body, err, stderr)
		}
	}
	if len(rec.seen()) != 0 {
		t.Fatal("invalid operation contacted server", rec.seen())
	}
}
