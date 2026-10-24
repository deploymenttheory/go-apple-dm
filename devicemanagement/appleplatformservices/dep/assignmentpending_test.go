package dep_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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

// RoundTrip loses one response after the remote mutation has completed.
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

// RoundTrip replaces one accepted serial outcome while retaining the rest of the response.
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

// TestPendingReadbackStoreFailures keeps a remotely accepted batch uncertain
// until its observations and outcomes can commit together. A failure on the
// second serial must roll back the first serial and its success event too.
func TestPendingReadbackStoreFailures(t *testing.T) {
	for _, fault := range []struct {
		name   string
		method string
		after  int
	}{
		{"list", "ListAssignments", 1},
		{"renew", "LockAccount", 2},
		{"commit_fence", "LockAccount", 3},
		{"second_device_read", "GetDevice", 2},
		{"second_device_write", "PutDevices", 2},
		{"second_outcome_read", "GetAssignment", 2},
		{"second_outcome_write", "PutAssignment", 2},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f := newAssignFixture(t)
			f.srv.AddDevices(device("A"), device("B"))
			f.sync(t)
			target := defineChoice(t, f, "explicit batch")
			f.putAccount(func(a *dep.Account) { a.ProfileTargets = map[string]string{"A": target, "B": target} })
			beforeAccount := f.account()
			transport := &lostAssignmentResponse{base: f.srv.Client().Transport}
			client, err := dep.NewClient(dep.ClientConfig{Store: f.store, Clock: f.clk, BaseURL: f.srv.URL(), HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			initial := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client })
			if _, err := initial.RunOnce(t.Context()); err == nil {
				t.Fatal("expected lost response after accepted batch")
			}
			beforeAssignments := []*dep.Assignment{assignment(t, f.store, "A"), assignment(t, f.store, "B")}
			beforeDevices := make([]*dep.StoredDevice, 0, 2)
			for _, serial := range []string{"A", "B"} {
				observed, err := f.store.GetDevice(t.Context(), acct, serial)
				if err != nil {
					t.Fatal(err)
				}
				beforeDevices = append(beforeDevices, observed)
				if remote, ok := f.srv.Device(serial); !ok || remote.ProfileUUID != target {
					t.Fatal("remote mutation was not accepted")
				}
			}
			down := errors.New("reconciliation storage unavailable")
			failing := &deptest.Failing{Store: f.store, Fail: map[string]error{fault.method: down}, After: map[string]int{fault.method: fault.after}}
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Store = failing })
			if _, err := worker.RunOnce(t.Context()); !errors.Is(err, down) {
				t.Fatalf("missing storage error: %v", err)
			}
			for i, serial := range []string{"A", "B"} {
				observed, err := f.store.GetDevice(t.Context(), acct, serial)
				if err != nil || !reflect.DeepEqual(observed, beforeDevices[i]) {
					t.Fatal("failed reconciliation committed an observation", err)
				}
				if got := assignment(t, f.store, serial); got.Status != dep.AssignmentPending || !reflect.DeepEqual(got, beforeAssignments[i]) {
					t.Fatal("failed reconciliation changed pending outcome")
				}
			}
			if !reflect.DeepEqual(beforeAccount, f.account()) || len(f.eventsOf(dep.EventDeviceAssigned)) != 0 {
				t.Fatal("failed reconciliation changed desired state or emitted success")
			}
			failing.SetFail(nil)
			if fault.method == "LockAccount" {
				// A failed ownership check also prevents lease release. Recovery
				// must respect that durable lease before inspecting the batch.
				if _, err := worker.RunOnce(t.Context()); !errors.Is(err, dep.ErrBackoff) {
					t.Fatalf("failed lock bypassed lease: %v", err)
				}
				f.clock.Advance(3 * time.Minute)
			}
			if _, err := worker.RunOnce(t.Context()); err != nil {
				t.Fatal("safe retry failed", err)
			}
			for _, serial := range []string{"A", "B"} {
				got := assignment(t, f.store, serial)
				observed, err := f.store.GetDevice(t.Context(), acct, serial)
				if err != nil || got.Status != dep.StatusSuccess || got.ProfileUUID != target || observed.ProfileUUID != target {
					t.Fatal("retry did not retain accepted target", err)
				}
			}
			if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
				t.Fatalf("duplicated accepted mutation: %d", n)
			}
			if len(f.eventsOf(dep.EventDeviceAssigned)) != 2 || !reflect.DeepEqual(beforeAccount, f.account()) {
				t.Fatal("retry changed desired state or lost success events")
			}
		})
	}
}

// TestPendingReadbackPagination settles every page of accepted pending requests
// without treating the shrinking pending result set as a reason to skip a serial.
func TestPendingReadbackPagination(t *testing.T) {
	f := newAssignFixture(t)
	f.srv.AddDevices(device("A"), device("B"), device("C"))
	f.sync(t)
	transport := &lostAssignmentResponse{base: f.srv.Client().Transport}
	client, err := dep.NewClient(dep.ClientConfig{Store: f.store, Clock: f.clk, BaseURL: f.srv.URL(), HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	initial := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client })
	if _, err := initial.RunOnce(t.Context()); err == nil {
		t.Fatal("expected lost response")
	}
	worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.PageSize = 1; c.BatchSize = 1 })
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, serial := range []string{"A", "B", "C"} {
		if got := assignment(t, f.store, serial); got.Status != dep.StatusSuccess {
			t.Fatalf("pending page skipped: %s", serial)
		}
	}
	if f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1 || f.srv.Count(http.MethodPost, dep.PathDeviceDetails) != 3 || len(f.eventsOf(dep.EventDeviceAssigned)) != 3 {
		t.Fatal("pagination duplicated a mutation or skipped readback/events")
	}
}

// TestPendingPersistenceFailurePreventsMutation verifies the uncertainty record
// is durable for the entire batch before Apple can receive an assignment.
func TestPendingPersistenceFailurePreventsMutation(t *testing.T) {
	for _, fault := range []struct {
		method string
		after  int
	}{
		{"LockAccount", 3},   // claim, renew, then pending transaction
		{"GetAssignment", 4}, // two candidates, then the second pending serial
	} {
		t.Run(fault.method, func(t *testing.T) {
			f := newAssignFixture(t)
			f.srv.AddDevices(device("A"), device("B"))
			f.sync(t)
			beforeAccount := f.account()
			down := errors.New("pending persistence unavailable")
			failing := &deptest.Failing{Store: f.store, Fail: map[string]error{fault.method: down}, After: map[string]int{fault.method: fault.after}}
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Store = failing })
			if _, err := worker.RunOnce(t.Context()); !errors.Is(err, down) {
				t.Fatalf("missing persistence error: %v", err)
			}
			for _, serial := range []string{"A", "B"} {
				if _, err := f.store.GetAssignment(t.Context(), acct, serial); !errors.Is(err, dep.ErrNotFound) {
					t.Fatal("partial pending batch committed", err)
				}
			}
			if f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 0 || len(f.eventsOf(dep.EventDeviceAssigned)) != 0 || !reflect.DeepEqual(beforeAccount, f.account()) {
				t.Fatal("failed persistence allowed a mutation, success event or desired-state change")
			}
			failing.SetFail(nil)
			// The injected lock failure also blocks release of the claimed lease.
			f.clock.Advance(3 * time.Minute)
			result, err := worker.RunOnce(t.Context())
			if err != nil || result.Assigned != 2 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1 {
				t.Fatalf("retry after persistence recovery: %+v %v", result, err)
			}
		})
	}
}
