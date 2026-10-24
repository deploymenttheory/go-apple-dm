package dep_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

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

// incompleteAssignmentResponse lets Apple accept the batch, then hides or
// replaces one serial's outcome while retaining the other terminal results.
type incompleteAssignmentResponse struct {
	base   http.RoundTripper
	status string
	used   bool
}

func (p *incompleteAssignmentResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := p.base.RoundTrip(r)
	if err != nil || r.URL.Path != dep.PathProfileDevs || p.used {
		return response, err
	}
	p.used = true
	raw, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	var assignment dep.AssignResponse
	if err := dep.Unmarshal(raw, &assignment); err != nil {
		return nil, err
	}
	if p.status == "" {
		delete(assignment.Devices, "A")
	} else {
		assignment.Devices["A"] = p.status
	}
	raw, err = dep.Marshal(assignment)
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	response.Header.Set("Content-Length", strconv.Itoa(len(raw)))
	return response, nil
}

// TestIncompleteAssignmentRequiresReadback uses a real accepted mutation with
// an incomplete success response. Neither elapsed retry time nor a target edit
// may bypass missing/inaccessible readback, including with ReadBack disabled.
func TestIncompleteAssignmentRequiresReadback(t *testing.T) {
	for _, status := range []string{"", "WEIRD_STATUS"} {
		for _, changeTarget := range []bool{false, true} {
			for _, readback := range []struct {
				name   string
				status int
				body   string
			}{
				{"missing", http.StatusOK, `{"devices":{}}`},
				{"inaccessible", http.StatusOK, `{"devices":{"A":{"response_status":"NOT_ACCESSIBLE"}}}`},
				{"unavailable", http.StatusServiceUnavailable, ``},
			} {
				t.Run(fmt.Sprintf("status_%q_target_change_%t_%s", status, changeTarget, readback.name), func(t *testing.T) {
					f := newAssignFixture(t)
					f.srv.AddDevices(device("A"), device("B"))
					f.sync(t)
					transport := &incompleteAssignmentResponse{base: f.srv.Client().Transport, status: status}
					client, err := dep.NewClient(dep.ClientConfig{Store: f.store, Clock: f.clk, BaseURL: f.srv.URL(), HTTPClient: &http.Client{Transport: transport}})
					if err != nil {
						t.Fatal(err)
					}
					worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client })
					result, err := worker.RunOnce(t.Context())
					if err != nil || result.Pending != 1 || result.Assigned != 1 || result.Failed != 0 {
						t.Fatalf("partial outcome: %+v %v", result, err)
					}
					pending := assignment(t, f.store, "A")
					if pending.Status != dep.AssignmentPending || pending.Attempts != 0 || !pending.NextAttemptAt.IsZero() {
						t.Fatalf("lost uncertainty: %+v", pending)
					}
					if status != "" && pending.LastError != status {
						t.Fatal("unknown status diagnostic lost")
					}
					if confirmed := assignment(t, f.store, "B"); confirmed.Status != dep.StatusSuccess {
						t.Fatal("known success not committed")
					}
					expected := f.profile
					if changeTarget {
						expected = defineChoice(t, f, "new target")
						account := f.account()
						account.ProfileTargets = map[string]string{"A": expected}
						if err := f.store.PutAccount(t.Context(), account); err != nil {
							t.Fatal(err)
						}
					}
					// Advancing beyond all failure backoffs must still require readback.
					f.clock.Advance(48 * time.Hour)
					f.srv.Script(dep.PathDeviceDetails, deptest.Scripted{Status: readback.status, Body: readback.body})
					if _, err := worker.RunOnce(t.Context()); err == nil {
						t.Fatal("uncertain assignment ignored unavailable readback")
					}
					if pending := assignment(t, f.store, "A"); pending.Status != dep.AssignmentPending {
						t.Fatal("failed readback cleared uncertainty")
					}
					if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
						t.Fatalf("mutation repeated before readback: %d", n)
					}
					result, err = worker.RunOnce(t.Context())
					expectedAssignments := 0
					expectedMutations := 1
					if changeTarget {
						expectedAssignments = 1
						expectedMutations = 2
					}
					if err != nil || result.Assigned != expectedAssignments {
						t.Fatalf("recovery: %+v %v", result, err)
					}
					if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != expectedMutations {
						t.Fatalf("duplicated accepted mutation: %d", n)
					}
					if remote, _ := f.srv.Device("A"); remote.ProfileUUID != expected {
						t.Fatal("recovery lost current target")
					}
					if got := assignment(t, f.store, "A"); got.Status != dep.StatusSuccess || got.ProfileUUID != expected {
						t.Fatalf("recovery outcome: %+v", got)
					}
					mutations, inspections := 0, 0
					for _, request := range f.srv.Requests() {
						if request.Path == dep.PathDeviceDetails {
							inspections++
						}
						if request.Path == dep.PathProfileDevs {
							mutations++
							if mutations > 1 && inspections < 2 {
								t.Fatal("changed-target mutation preceded successful readback")
							}
						}
					}
					if events := f.eventsOf(dep.EventDeviceAssigned); len(events) != 2 {
						t.Fatalf("unexpected success events: %d", len(events))
					}
				})
			}
		}
	}
}
