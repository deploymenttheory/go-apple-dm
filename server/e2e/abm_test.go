//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm/axmtest"
)

// TestE2E_ABMAssignDevices is E2E-021: against our fake Business Manager,
// list servers and devices with paging, assign devices to our server,
// wait for the activity and the assignment to converge, read the audit
// events, unassign; then prove the token expiry replay and Retry-After.
func TestE2E_ABMAssignDevices(t *testing.T) {
	ctx := context.Background()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fake := axmtest.NewServer()
	t.Cleanup(fake.Close)
	fake.RegisterKey("BUSINESSAPI.e2e", "kid-e2e", &key.PublicKey)
	server := fake.AddMDMServer("go-apple-dm", nil)
	serials := []string{"C02E2E0001", "C02E2E0002", "C02E2E0003"}
	for _, s := range serials {
		fake.AddOrgDevice(s, nil)
	}
	fake.SetConsistencyLag(50 * time.Millisecond)
	fake.AutoAdvance(10 * time.Millisecond)
	client, err := axm.New(ctx, axm.Config{ClientID: "BUSINESSAPI.e2e", KeyID: "kid-e2e", PrivateKey: key, BaseURL: fake.URL, TokenURL: fake.TokenURL, HTTPClient: fake.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// Paging: two pages of devices.
	var seen []string
	first, err := client.ListOrgDevices(ctx, axm.ListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	for page, err := range axm.Pages(ctx, client, first) {
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range page.Items {
			seen = append(seen, d.ID)
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, serials) {
		t.Fatalf("paged devices = %v", seen)
	}
	servers, err := client.ListMDMServers(ctx, axm.ListOptions{})
	if err != nil || len(servers.Items) != 1 || servers.Items[0].ID != server {
		t.Fatalf("servers = %+v %v", servers, err)
	}

	// Assign and observe completion, convergence, and the log.
	act, err := client.AssignDevices(ctx, server, serials)
	if err != nil || act.Attributes.Status != axm.ActivityInProgress {
		t.Fatalf("assign = %+v %v", act, err)
	}
	done, err := client.WaitForActivity(ctx, act.ID, axm.WaitOptions{Interval: 10 * time.Millisecond, Timeout: 10 * time.Second})
	if err != nil || done.Attributes.Status != axm.ActivityCompleted || done.Attributes.DownloadURL == "" {
		t.Fatalf("wait = %+v %v", done, err)
	}
	log, err := client.FetchActivityLog(ctx, done.Attributes.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	csv, _ := io.ReadAll(log)
	log.Close()
	if len(csv) == 0 {
		t.Fatal("empty activity log")
	}
	for _, s := range serials {
		if err := client.WaitForAssignedServer(ctx, s, server, 5*time.Second); err != nil {
			t.Fatalf("%s did not converge: %v", s, err)
		}
	}
	linked, err := client.ListMDMServerDevices(ctx, server, axm.ListOptions{})
	if err != nil || len(linked.Items) != 3 {
		t.Fatalf("server devices = %+v %v", linked, err)
	}
	events, err := client.ListAuditEvents(ctx, axm.AuditEventQuery{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	assigned := 0
	for _, e := range events.Items {
		if e.Attributes.Type == axm.AuditDeviceAssignedToServer {
			assigned++
		}
	}
	if assigned != 3 {
		t.Fatalf("audit events: %d assignments, want 3", assigned)
	}

	// Unassign and observe the reverse.
	act, err = client.UnassignDevices(ctx, serials[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.WaitForActivity(ctx, act.ID, axm.WaitOptions{Interval: 10 * time.Millisecond, Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for fake.AssignedServer(serials[0]) != "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fake.AssignedServer(serials[0]) != "" {
		t.Fatal("device still assigned after unassign")
	}

	// Expired tokens: the client replays once with a fresh token.
	tokensBefore := fake.TokenRequests()
	fake.ExpireTokens()
	if _, err := client.ListMDMServers(ctx, axm.ListOptions{}); err != nil {
		t.Fatalf("after token expiry: %v", err)
	}
	if fake.TokenRequests() != tokensBefore+1 {
		t.Fatalf("token requests = %d, want one refresh", fake.TokenRequests()-tokensBefore)
	}

	// Rate limit: Retry-After is honoured and the call still succeeds.
	fake.RateLimit(1, "1")
	start := time.Now()
	if _, err := client.ListMDMServers(ctx, axm.ListOptions{}); err != nil {
		t.Fatalf("after rate limit: %v", err)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("Retry-After not honoured: %v", time.Since(start))
	}
	var apiErr *axm.Error
	if _, err := client.GetOrgDevice(ctx, "NOPE", axm.GetOptions{}); !errors.As(err, &apiErr) || !axm.IsNotFound(err) {
		t.Fatalf("unknown device = %v", err)
	}
}
