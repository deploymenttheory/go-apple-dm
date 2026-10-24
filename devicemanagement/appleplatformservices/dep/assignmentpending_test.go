package dep_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
)

// lostAssignmentResponse simulates Apple committing a mutation before the
// connection disappears, so retry safety cannot rely on a local success record.
type lostAssignmentResponse struct {
	base http.RoundTripper
	lost bool
}

func (l *lostAssignmentResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := l.base.RoundTrip(r)
	if err == nil && r.URL.Path == dep.PathProfileDevs && !l.lost {
		l.lost = true
		_ = response.Body.Close()
		return nil, errors.New("connection lost after mutation")
	}
	return response, err
}

// TestUncertainAssignmentReadback checks recovery without a duplicate remote
// mutation, and blocks resubmission while Apple readback is unavailable.
func TestUncertainAssignmentReadback(t *testing.T) {
	f := newAssignFixture(t)
	f.srv.AddDevices(device("A"), device("B"))
	f.sync(t)
	transport := &lostAssignmentResponse{base: f.srv.Client().Transport}
	client, err := dep.NewClient(dep.ClientConfig{Store: f.store, Clock: f.clk, BaseURL: f.srv.URL(), HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client; c.PageSize = 1; c.BatchSize = 1 })
	if _, err := worker.RunOnce(t.Context()); err == nil {
		t.Fatal("lost response accepted")
	}
	if asg := assignment(t, f.store, "A"); asg.Status != dep.AssignmentPending {
		t.Fatal("uncertainty not durable")
	}
	f.srv.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 503})
	if _, err := worker.RunOnce(t.Context()); err == nil {
		t.Fatal("unavailable readback accepted")
	}
	if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
		t.Fatal("blind retry during readback failure")
	}
	result, err := worker.RunOnce(t.Context())
	if err != nil || result.Assigned != 1 {
		t.Fatalf("recovery: %+v %v", result, err)
	}
	if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 2 {
		t.Fatalf("uncertain request repeated: %d", n)
	}
	if asg := assignment(t, f.store, "A"); asg.Status != dep.StatusSuccess {
		t.Fatal("readback success not retained")
	}
	if events := f.eventsOf(dep.EventDeviceAssigned); len(events) != 2 {
		t.Fatalf("events: %d", len(events))
	}
}

// TestAssignmentReadbackRejectsWrongSerial ensures keyed responses cannot write
// another physical device or falsely settle uncertainty for the requested one.
func TestAssignmentReadbackRejectsWrongSerial(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, serial := range []string{"", "OTHER"} {
			t.Run(fmt.Sprintf("pending_%t_serial_%q", pending, serial), func(t *testing.T) {
				f := newAssignFixture(t)
				f.srv.AddDevices(device("A"))
				f.sync(t)
				if err := f.store.PutDevices(t.Context(), acct, []dep.Device{device("OTHER")}, t0); err != nil {
					t.Fatal(err)
				}
				if pending {
					if err := f.store.PutAssignment(t.Context(), &dep.Assignment{Account: acct, SerialNumber: "A", ProfileUUID: f.profile, Status: dep.AssignmentPending}); err != nil {
						t.Fatal(err)
					}
				}
				body := fmt.Sprintf(`{"devices":{"A":{"serial_number":%q,"profile_uuid":%q,"profile_status":"assigned","response_status":"SUCCESS"}}}`, serial, f.profile)
				f.srv.Script(dep.PathDeviceDetails, deptest.Scripted{Status: http.StatusOK, Body: body})
				worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.ReadBack = true; c.BatchSize = 1 })
				if _, err := worker.RunOnce(t.Context()); !errors.Is(err, dep.ErrConflict) {
					t.Fatalf("bad identity accepted: %v", err)
				}
				other, err := f.store.GetDevice(t.Context(), acct, "OTHER")
				if err != nil || other.ProfileUUID != "" {
					t.Fatal("mismatched detail changed another serial", err)
				}
				asg := assignment(t, f.store, "A")
				expected := dep.StatusSuccess
				if pending {
					expected = dep.AssignmentPending
				}
				if asg.Status != expected {
					t.Fatalf("wrong serial changed uncertainty: %+v", asg)
				}
				observed, err := f.store.GetDevice(t.Context(), acct, "A")
				if err != nil || observed.ProfileUUID != "" {
					t.Fatal("mismatched observation committed", err)
				}
			})
		}
	}
}
