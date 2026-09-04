package dep_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
)

func TestEndpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("Account", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		d, err := f.client.Account(ctx, acct)
		if err != nil || d.OrgName != "Deployment Theory" || d.ServerUUID != "SERVER-UUID-DEPTEST" || len(d.URLs) != 4 || d.Limits()[dep.PathProfileDevs].Maximum != 1000 {
			t.Fatalf("%+v %v", d, err)
		}
	})

	t.Run("FetchDevices", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"), device("B"), device("C"))
		page, err := f.client.FetchDevices(ctx, acct, "", 2)
		if err != nil || len(page.Devices) != 2 || !page.MoreToFollow || page.Cursor == "" || page.FetchedUntil == nil {
			t.Fatalf("%+v %v", page, err)
		}
		if page.Devices[0].OpType != "" || page.Devices[0].OpDate != nil {
			t.Fatalf("fetch page carries op fields: %+v", page.Devices[0])
		}
		page, err = f.client.FetchDevices(ctx, acct, page.Cursor, 2)
		if err != nil || len(page.Devices) != 1 || page.MoreToFollow {
			t.Fatalf("second page: %+v %v", page, err)
		}
		_, err = f.client.FetchDevices(ctx, acct, page.Cursor, 2)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Code != dep.CodeExhaustedCursor {
			t.Fatalf("exhausted: %v", err)
		}
	})

	t.Run("SyncDevices", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		page, err := f.client.FetchDevices(ctx, acct, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		f.srv.AddDevices(device("B"))
		f.srv.DeleteDevice("A")
		sync, err := f.client.SyncDevices(ctx, acct, page.Cursor, 0)
		if err != nil || len(sync.Devices) != 2 || sync.Devices[0].OpType != dep.OpAdded || sync.Devices[1].OpType != dep.OpDeleted || sync.Devices[1].OpDate == nil {
			t.Fatalf("%+v %v", sync, err)
		}
		if _, err := f.client.SyncDevices(ctx, acct, "", 0); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty cursor: %v", err)
		}
	})

	t.Run("DeviceDetails", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		got, err := f.client.DeviceDetails(ctx, acct, []string{"A", "ZZZ"})
		if err != nil || got["A"].ResponseStatus != dep.StatusSuccess || got["A"].Model != "iPad" || got["ZZZ"].ResponseStatus != dep.StatusNotAccessible {
			t.Fatalf("%+v %v", got, err)
		}
		for _, serials := range [][]string{nil, {}, {"A", ""}} {
			if _, err := f.client.DeviceDetails(ctx, acct, serials); !errors.Is(err, dep.ErrInvalid) {
				t.Fatalf("%v: %v", serials, err)
			}
		}
	})

	t.Run("DisownDevices", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		got, err := f.client.DisownDevices(ctx, acct, []string{"A", "ZZZ"})
		if err != nil || got["A"] != dep.StatusSuccess || got["ZZZ"] != dep.StatusNotAccessible {
			t.Fatalf("%+v %v", got, err)
		}
		if _, ok := f.srv.Device("A"); ok {
			t.Fatal("disowned device still present")
		}
		if _, err := f.client.DisownDevices(ctx, acct, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("no serials: %v", err)
		}
	})

	t.Run("ActivationLock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		got, err := f.client.ActivationLock(ctx, acct, dep.ActivationLockRequest{Device: "A", EscrowKey: "k", LostMessage: "call IT"})
		if err != nil || got.SerialNumber != "A" || got.ResponseStatus != dep.StatusSuccess {
			t.Fatalf("%+v %v", got, err)
		}
		got, err = f.client.ActivationLock(ctx, acct, dep.ActivationLockRequest{Device: "ZZZ"})
		if err != nil || got.ResponseStatus != "DEVICE_NOT_FOUND" {
			t.Fatalf("unknown: %+v %v", got, err)
		}
		if _, err := f.client.ActivationLock(ctx, acct, dep.ActivationLockRequest{}); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty: %v", err)
		}
	})

	t.Run("DefineProfile", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		f.srv.Throttle("B", 30)
		p := &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m", Devices: []string{"A", "B", "ZZZ"}}
		resp, err := f.client.DefineProfile(ctx, acct, p)
		if err != nil || resp.ProfileUUID == "" || resp.Devices["A"] != dep.StatusSuccess || resp.Devices["ZZZ"] != dep.StatusNotAccessible {
			t.Fatalf("%+v %v", resp, err)
		}
		if d, _ := f.srv.Device("A"); d.ProfileUUID != resp.ProfileUUID || d.ProfileStatus != dep.ProfileStatusAssigned {
			t.Fatalf("device after define: %+v", d)
		}
		if _, err := f.client.DefineProfile(ctx, acct, nil); !errors.Is(err, dep.ErrProfileInvalid) {
			t.Fatalf("nil: %v", err)
		}
	})

	t.Run("AssignIsPostByDefault", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		resp, err := f.client.DefineProfile(ctx, acct, &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m"})
		if err != nil {
			t.Fatal(err)
		}
		f.srv.NotAccessible("N", true)
		f.srv.Fail("F", true)
		f.srv.AddDevices(device("N"), device("F"))
		got, err := f.client.AssignProfile(ctx, acct, resp.ProfileUUID, []string{"A", "N", "F"})
		if err != nil || got.ProfileUUID != resp.ProfileUUID || got.Devices["A"] != dep.StatusSuccess || got.Devices["N"] != dep.StatusNotAccessible || got.Devices["F"] != dep.StatusFailed || got.RetryAfterSeconds != 0 {
			t.Fatalf("%+v %v", got, err)
		}
		if f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1 || f.srv.Count(http.MethodPut, dep.PathProfileDevs) != 0 {
			t.Fatal("assign was not a POST")
		}
		if _, err := f.client.AssignProfile(ctx, acct, "", []string{"A"}); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("no uuid: %v", err)
		}
		if _, err := f.client.AssignProfile(ctx, acct, resp.ProfileUUID, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("no serials: %v", err)
		}
		_, err = f.client.AssignProfile(ctx, acct, "PROFILE-NOPE", []string{"A"})
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Status != http.StatusNotFound {
			t.Fatalf("unknown profile: %v", err)
		}
	})

	t.Run("AssignPutOption", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		resp, err := f.client.DefineProfile(ctx, acct, &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m"})
		if err != nil {
			t.Fatal(err)
		}
		f.srv.Throttle("A", 45)
		got, err := f.client.AssignProfile(ctx, acct, resp.ProfileUUID, []string{"A"}, dep.WithAssignPUT())
		if err != nil || got.Devices["A"] != dep.StatusThrottled || got.RetryAfterSeconds != 45 {
			t.Fatalf("%+v %v", got, err)
		}
		if f.srv.Count(http.MethodPut, dep.PathProfileDevs) != 1 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 0 {
			t.Fatal("assign was not a PUT")
		}
	})

	t.Run("RemoveProfile", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		if _, err := f.client.DefineProfile(ctx, acct, &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m", Devices: []string{"A"}}); err != nil {
			t.Fatal(err)
		}
		got, err := f.client.RemoveProfile(ctx, acct, []string{"A", "ZZZ"})
		if err != nil || got["A"] != dep.StatusSuccess || got["ZZZ"] != dep.StatusNotAccessible {
			t.Fatalf("%+v %v", got, err)
		}
		if d, _ := f.srv.Device("A"); d.ProfileUUID != "" || d.ProfileStatus != dep.ProfileStatusRemoved {
			t.Fatalf("device after remove: %+v", d)
		}
		if f.srv.Count(http.MethodDelete, dep.PathProfileDevs) != 1 {
			t.Fatal("remove was not a DELETE")
		}
		if _, err := f.client.RemoveProfile(ctx, acct, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("no serials: %v", err)
		}
	})

	t.Run("FetchProfile", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		resp, err := f.client.DefineProfile(ctx, acct, &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m"})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.client.FetchProfile(ctx, acct, resp.ProfileUUID)
		if err != nil || p.ProfileUUID != resp.ProfileUUID || p.ProfileName != "n" {
			t.Fatalf("%+v %v", p, err)
		}
		_, err = f.client.FetchProfile(ctx, acct, "PROFILE-NOPE")
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Code != dep.CodeNotFound {
			t.Fatalf("unknown: %v", err)
		}
		if _, err := f.client.FetchProfile(ctx, acct, ""); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty: %v", err)
		}
	})

	t.Run("AssignAccountDrivenEnrollmentDiscovery", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		if err := f.client.AssignAccountDrivenEnrollmentDiscovery(ctx, acct, "https://mdm.example.com/.well-known/com.apple.remotemanagement"); err != nil {
			t.Fatal(err)
		}
		if f.srv.DiscoveryURL() != "https://mdm.example.com/.well-known/com.apple.remotemanagement" {
			t.Fatalf("stored %q", f.srv.DiscoveryURL())
		}
		err := f.client.AssignAccountDrivenEnrollmentDiscovery(ctx, acct, "http://insecure.example.com")
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Code != dep.CodeDiscoveryInvalid {
			t.Fatalf("http: %v", err)
		}
		if err := f.client.AssignAccountDrivenEnrollmentDiscovery(ctx, acct, ""); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty: %v", err)
		}
	})

	t.Run("FetchAccountDrivenEnrollmentDiscovery", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		_, err := f.client.FetchAccountDrivenEnrollmentDiscovery(ctx, acct)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Status != http.StatusNotFound || derr.Code != dep.CodeNotFound {
			t.Fatalf("none: %v", err)
		}
		if err := f.client.AssignAccountDrivenEnrollmentDiscovery(ctx, acct, "https://mdm.example.com/d"); err != nil {
			t.Fatal(err)
		}
		if got, err := f.client.FetchAccountDrivenEnrollmentDiscovery(ctx, acct); err != nil || got != "https://mdm.example.com/d" {
			t.Fatalf("%q %v", got, err)
		}
	})

	t.Run("RemoveAccountDrivenEnrollmentDiscovery", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		if err := f.client.AssignAccountDrivenEnrollmentDiscovery(ctx, acct, "https://mdm.example.com/d"); err != nil {
			t.Fatal(err)
		}
		if err := f.client.RemoveAccountDrivenEnrollmentDiscovery(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if f.srv.DiscoveryURL() != "" {
			t.Fatal("discovery URL survived removal")
		}
		if _, err := f.client.FetchAccountDrivenEnrollmentDiscovery(ctx, acct); err == nil {
			t.Fatal("fetch after remove succeeded")
		}
	})

	t.Run("BetaTokensForbiddenTyped", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.SetSeedForITOff(true)
		_, err := f.client.BetaEnrollmentTokens(ctx, acct)
		var derr *dep.Error
		if !errors.Is(err, dep.ErrSeedForITOff) || !errors.As(err, &derr) || derr.Status != http.StatusForbidden {
			t.Fatalf("err = %v", err)
		}
		f.srv.SetSeedForITOff(false)
		f.srv.SetBetaTokens([]dep.BetaToken{{OS: "iOS", Title: "iOS 27 Public Beta", Token: "tok"}})
		got, err := f.client.BetaEnrollmentTokens(ctx, acct)
		if err != nil || len(got) != 1 || got[0].Token != "tok" {
			t.Fatalf("%+v %v", got, err)
		}
		// The older seedBuildTokens key is honoured when the new one is
		// absent.
		f.srv.Script(dep.PathBetaTokens, deptest.Scripted{Status: 200, Body: `{"seedBuildTokens":[{"os":"OSX","title":"old","token":"t2"}]}`})
		got, err = f.client.BetaEnrollmentTokens(ctx, acct)
		if err != nil || len(got) != 1 || got[0].Token != "t2" {
			t.Fatalf("seedBuildTokens: %+v %v", got, err)
		}
		f.srv.Script(dep.PathBetaTokens, deptest.Scripted{Status: 500})
		if _, err := f.client.BetaEnrollmentTokens(ctx, acct); !errors.As(err, &derr) || derr.Status != 500 || errors.Is(err, dep.ErrSeedForITOff) {
			t.Fatalf("500: %v", err)
		}
	})
}
