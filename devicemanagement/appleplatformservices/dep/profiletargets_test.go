package dep_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

// defineChoice retains caller profiles independently of account defaults.
func defineChoice(t *testing.T, f *assignFixture, name string) string {
	t.Helper()
	p := &dep.Profile{ProfileName: name, URL: "https://mdm.example.com", OrgMagic: name}
	response, err := f.client.DefineProfile(t.Context(), acct, p)
	if err != nil {
		t.Fatal(err)
	}
	p.ProfileUUID = response.ProfileUUID
	if err := f.store.PutProfile(t.Context(), acct, p); err != nil {
		t.Fatal(err)
	}
	return response.ProfileUUID
}

// TestPerSerialProfilesReconcile checks that sync preserves explicit choices and
// batches are homogeneous by UUID regardless of device family/OS.
func TestPerSerialProfilesReconcile(t *testing.T) {
	for _, defaultEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "no default", true: "default"}[defaultEnabled], func(t *testing.T) {
			f := newAssignFixture(t)
			if err := f.store.PutProfile(t.Context(), acct, &dep.Profile{ProfileUUID: f.profile, ProfileName: "Default", URL: "https://mdm.example.com"}); err != nil {
				t.Fatal(err)
			}
			second := defineChoice(t, f, "Second")
			third := defineChoice(t, f, "Third")
			a := f.account()
			if !defaultEnabled {
				a.ProfileUUID = ""
			}
			a.ProfileTargets = map[string]string{"A": second, "B": third, "C": second}
			if err := f.store.PutAccount(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			f.srv.AddDevices(device("A"), device("B"), device("C"), device("D"))
			f.sync(t)
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.ReadBack = true; c.BatchSize = 2; c.PageSize = 1 })
			result, err := worker.RunOnce(t.Context())
			want := 3
			if defaultEnabled {
				want++
			}
			if err != nil || result.Assigned != want {
				t.Fatalf("mixed assignment: %+v %v", result, err)
			}
			for serial, uuid := range map[string]string{"A": second, "B": third, "C": second} {
				remote, _ := f.srv.Device(serial)
				stored, err := f.store.GetDevice(t.Context(), acct, serial)
				if err != nil || remote.ProfileUUID != uuid || stored.ProfileUUID != uuid {
					t.Fatalf("%s: remote=%s stored=%+v %v", serial, remote.ProfileUUID, stored, err)
				}
			}
			remote, _ := f.srv.Device("D")
			expected := ""
			if defaultEnabled {
				expected = f.profile
			}
			if remote.ProfileUUID != expected {
				t.Fatal("implicit family/OS profile selection")
			}
			a = f.account()
			delete(a.ProfileTargets, "A")
			if err := f.store.PutAccount(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			remote, _ = f.srv.Device("A")
			if remote.ProfileUUID != second {
				t.Fatal("removal mutated Apple state")
			}
			f.sync(t)
			result, err = worker.RunOnce(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			remote, _ = f.srv.Device("A")
			if defaultEnabled && (remote.ProfileUUID != f.profile || result.Assigned != 1) {
				t.Fatal("override did not fall back")
			}
			if !defaultEnabled && (remote.ProfileUUID != second || result.Assigned != 0) {
				t.Fatal("no policy cleared Apple profile")
			}
			if n := f.srv.Count(http.MethodDelete, dep.PathProfileDevs); n != 0 {
				t.Fatal("implicit profile removal")
			}
			// Renewing credentials for the same account preserves explicit choices.
			if _, err := f.client.StoreTokens(t.Context(), acct, f.srv.Tokens()); err != nil {
				t.Fatal(err)
			}
			a = f.account()
			if a.ProfileTargets["B"] != third || a.ProfileTargets["C"] != second {
				t.Fatal("renewal lost targets")
			}
		})
	}
}

// TestSerialTargetChangeFencesReplies covers target/default ABA and selected
// definition edits during assignment and readback requests.
func TestSerialTargetChangeFencesReplies(t *testing.T) {
	for _, mode := range []string{"target", "target ABA", "default ABA", "definition", "readback"} {
		t.Run(mode, func(t *testing.T) {
			f := newAssignFixture(t)
			if err := f.store.PutProfile(t.Context(), acct, &dep.Profile{ProfileUUID: f.profile, ProfileName: "Default", URL: "https://mdm.example.com"}); err != nil {
				t.Fatal(err)
			}
			second := defineChoice(t, f, "Second")
			a := f.account()
			a.ProfileTargets = map[string]string{"A": second}
			if err := f.store.PutAccount(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			f.srv.AddDevices(device("A"), device("B"))
			f.sync(t)
			path := dep.PathProfileDevs
			if mode == "readback" {
				path = dep.PathDeviceDetails
			}
			client, entered, release := gateClient(t, f.fixture, path)
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client; c.BatchSize = 1; c.ReadBack = mode == "readback" })
			done := make(chan error, 1)
			go func() { _, err := worker.RunOnce(t.Context()); done <- err }()
			awaitRequest(t, entered)
			a = f.account()
			switch mode {
			case "target", "readback":
				a.ProfileTargets["A"] = f.profile
			case "target ABA":
				a.ProfileTargets["A"] = f.profile
				if err := f.store.PutAccount(t.Context(), a); err != nil {
					t.Fatal(err)
				}
				a.ProfileTargets["A"] = second
			case "default ABA":
				a.ProfileUUID = ""
				if err := f.store.PutAccount(t.Context(), a); err != nil {
					t.Fatal(err)
				}
				a.ProfileUUID = f.profile
			case "definition":
				if err := f.store.PutProfile(t.Context(), acct, &dep.Profile{ProfileUUID: second, ProfileName: "edited", URL: "https://mdm.example.com"}); err != nil {
					t.Fatal(err)
				}
				a = f.account()
			}
			if err := f.store.PutAccount(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; !errors.Is(err, dep.ErrConflict) {
				t.Fatalf("stale response: %v", err)
			}
			if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
				t.Fatalf("continued stale batches: %d", n)
			}
			if mode != "readback" {
				got, err := f.store.GetAssignment(t.Context(), acct, "B")
				if err != nil || got.Status != dep.AssignmentPending {
					t.Fatalf("stale business outcome: %+v %v", got, err)
				}
			}
		})
	}
}

// TestSerialTargetEditRetainsAppleThrottle checks that changing desired profile
// cannot bypass Apple's per-device cooldown, including fallback to the default.
func TestSerialTargetEditRetainsAppleThrottle(t *testing.T) {
	f := newAssignFixture(t)
	if err := f.store.PutProfile(t.Context(), acct, &dep.Profile{ProfileUUID: f.profile, ProfileName: "Default"}); err != nil {
		t.Fatal(err)
	}
	second := defineChoice(t, f, "second")
	a := f.account()
	a.ProfileTargets = map[string]string{"A": second}
	if err := f.store.PutAccount(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	f.srv.AddDevices(device("A"))
	f.sync(t)
	f.srv.Throttle("A", 90)
	worker := newAssigner(t, f)
	result, err := worker.RunOnce(t.Context())
	if err != nil || result.Throttled != 1 {
		t.Fatalf("throttle: %+v %v", result, err)
	}
	a = f.account()
	delete(a.ProfileTargets, "A")
	if err := f.store.PutAccount(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	result, err = worker.RunOnce(t.Context())
	if err != nil || result.Deferred != 1 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1 {
		t.Fatalf("target bypassed throttle: %+v %v", result, err)
	}
	f.clock.Advance(91 * time.Second)
	f.srv.Throttle("A", 0)
	result, err = worker.RunOnce(t.Context())
	if err != nil || result.Assigned != 1 {
		t.Fatalf("retry: %+v %v", result, err)
	}
	if remote, _ := f.srv.Device("A"); remote.ProfileUUID != f.profile {
		t.Fatal("retry lost current desired target")
	}
}

func ExampleAccount_DesiredProfile() {
	a := &dep.Account{ProfileUUID: "default-uuid", ProfileTargets: map[string]string{"SERIAL-A": "selected-uuid"}}
	fmt.Println(a.DesiredProfile("SERIAL-A"))
	fmt.Println(a.DesiredProfile("SERIAL-B"))
	delete(a.ProfileTargets, "SERIAL-A")
	fmt.Println(a.DesiredProfile("SERIAL-A"))
	a.ProfileUUID = ""
	fmt.Println(a.DesiredProfile("SERIAL-A") == "")
	// Output:
	// selected-uuid
	// default-uuid
	// default-uuid
	// true
}
