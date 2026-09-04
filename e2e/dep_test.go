//go:build e2e

package e2e

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/gdmf/gdmftest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	depinmem "github.com/deploymenttheory/go-apple-dm/v3/storage/dep/inmem"
)

// TestE2E_DEPAssign is E2E-011: the token PKI exchange with our fake DEP
// service, fetch then sync with cursor expiry, a profile defined and
// assigned by the state-driven assigner, the device enrolling through ADE
// with verified MachineInfo joined to its DEP record, and the software
// update gate answering 403 for an old OS.
func TestE2E_DEPAssign(t *testing.T) {
	ctx := context.Background()
	const account = "abm"
	clk := clock.NewFake(t0)
	fake := deptest.NewServer(deptest.Options{Clock: clk})
	t.Cleanup(fake.Close)
	store := depinmem.New()
	client, err := dep.NewClient(dep.ClientConfig{Store: store, BaseURL: fake.URL(), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}

	// The ADE endpoint joins MachineInfo to the DEP record and gates old OS
	// versions.
	var joined []string
	f := newADEFixture(t, func(c *ade.Config) {
		c.DEP = ade.DEPLookupFunc(func(ctx context.Context, serial string) (any, bool, error) {
			d, err := store.GetDevice(ctx, account, serial)
			if errors.Is(err, dep.ErrNotFound) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, err
			}
			joined = append(joined, serial)
			return d, true, nil
		})
		c.SoftwareUpdate = ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			return ade.Target{OSVersion: "26.0"}, true, nil
		})
		c.GDMF = gdmftest.NewFake("Mac16,1")
	})

	// Token PKI: generate the keypair, let the portal (the fake) encrypt the
	// token to it, import the .p7m.
	kp, err := dep.GenerateTokenPKI("go-apple-dm", 24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutKeypair(ctx, account, dep.StageStaged, kp); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(kp.CertPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p7m, err := fake.TokenP7M(cert)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := client.ImportToken(ctx, account, p7m, dep.ImportOptions{})
	if err != nil {
		t.Fatalf("import token: %v", err)
	}
	if detail.ServerUUID == "" {
		t.Fatalf("account detail = %+v", detail)
	}
	if _, err := store.Keypair(ctx, account, dep.StageCurrent); err != nil {
		t.Fatalf("keypair not upstaged: %v", err)
	}

	// Fetch, then sync with changes.
	fake.AddDevices(dep.Device{SerialNumber: "C02DEP0001", Model: "MacBook Pro", DeviceFamily: "Mac", OS: "OSX"}, dep.Device{SerialNumber: "C02DEP0002", Model: "iPad", DeviceFamily: "iPad", OS: "iOS"})
	syncer, err := dep.NewSyncer(dep.SyncerConfig{Client: client, Store: store, Account: account, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	res, err := syncer.RunOnce(ctx)
	if err != nil || res.Added != 2 {
		t.Fatalf("fetch = %+v %v", res, err)
	}
	fake.AddDevices(dep.Device{SerialNumber: "C02DEP0003", Model: "MacBook Air", DeviceFamily: "Mac", OS: "OSX"})
	res, err = syncer.RunOnce(ctx)
	if err != nil || res.Added != 1 || res.Phase != dep.PhaseSync {
		t.Fatalf("sync = %+v %v", res, err)
	}
	// An eight-day-old cursor is discarded and the fetch restarts.
	clk.Advance(8 * 24 * time.Hour)
	res, err = syncer.RunOnce(ctx)
	if err != nil || !res.Restarted {
		t.Fatalf("stale cursor = %+v %v", res, err)
	}

	// Define the profile pointing at our ADE endpoint and assign.
	profile := &dep.Profile{ProfileName: "go-apple-dm e2e", URL: f.server.URL + "/ade", OrgMagic: "e2e", AwaitDeviceConfigured: new(true), IsSupervised: new(true), IsMDMRemovable: new(false)}
	resp, err := client.DefineProfile(ctx, account, profile)
	if err != nil {
		t.Fatalf("define profile: %v", err)
	}
	if err := store.PutProfile(ctx, account, profile); err != nil {
		t.Fatal(err)
	}
	acct, err := store.GetAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	acct.ProfileUUID = resp.ProfileUUID
	if err := store.PutAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	assigner, err := dep.NewAssigner(dep.AssignerConfig{Client: client, Store: store, Account: account, Clock: clk, ReadBack: true})
	if err != nil {
		t.Fatal(err)
	}
	ares, err := assigner.RunOnce(ctx)
	if err != nil || ares.Assigned != 3 {
		t.Fatalf("assign = %+v %v", ares, err)
	}
	for _, serial := range []string{"C02DEP0001", "C02DEP0002", "C02DEP0003"} {
		if d, ok := fake.Device(serial); !ok || d.ProfileUUID != resp.ProfileUUID || d.ProfileStatus != dep.ProfileStatusAssigned {
			t.Fatalf("%s at the service = %+v", serial, d)
		}
	}
	// A second run finds nothing to do.
	if ares, err := assigner.RunOnce(ctx); err != nil || ares.Candidates != 0 {
		t.Fatalf("idle assign = %+v %v", ares, err)
	}

	// The Mac enrols through ADE; its MachineInfo is joined to the DEP record.
	mac := f.device("UDID-DEP-1")
	mac.SerialNumber, mac.ProductName, mac.OSVersion = "C02DEP0001", "Mac16,1", "26.0"
	if err := mac.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{CanRequestSoftwareUpdate: true}); err != nil {
		t.Fatalf("ADE enrol: %v", err)
	}
	if len(joined) != 1 || joined[0] != "C02DEP0001" {
		t.Fatalf("DEP join = %v", joined)
	}
	if e, err := f.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-DEP-1"}); err != nil || !e.Enabled {
		t.Fatalf("enrollment = %+v %v", e, err)
	}
	// An old OS that can request updates is told to update first.
	old := f.device("UDID-DEP-3")
	old.SerialNumber, old.ProductName, old.OSVersion = "C02DEP0003", "Mac16,1", "15.0"
	var sur *simulator.SoftwareUpdateRequired
	if err := old.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{CanRequestSoftwareUpdate: true}); !errors.As(err, &sur) || sur.OSVersion != "26.0" {
		t.Fatalf("software update gate = %v", err)
	}
	// One that cannot request updates enrols regardless.
	if err := old.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{}); err != nil {
		t.Fatalf("gate bypass = %v", err)
	}
}
