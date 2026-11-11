package inventorystore_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

type inventoryCooldownTransport struct{ calls int }

// RoundTrip records provider access and supplies an explicit token-endpoint throttle.
func (t *inventoryCooldownTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_request","error_description":"private-diagnostic"}`)), Request: r}, nil
}

// checkInventoryCooldown is shared by SQLite, PostgreSQL and MySQL. Independent
// repository instances must respect the same encrypted durable account deadline.
func checkInventoryCooldown(t *testing.T, db *sql.DB, d sqlcommon.Dialect, keys *crypt.Keyring) {
	t.Helper()
	store, err := inventorystore.Open(t.Context(), db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := inventory.New(store)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	a, err := repo.SaveAccount(t.Context(), inventory.Account{ID: inventory.ID(), Name: "Cooldown", ClientID: "BUSINESSAPI.fixture", KeyID: "kid", Enabled: true}, private, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	transport := &inventoryCooldownTransport{}
	factory := func(ctx context.Context, a inventory.Account, key []byte) (*axm.Client, error) {
		return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, HTTPClient: &http.Client{Transport: transport}, Retry: axm.Retry{Base: time.Millisecond, Cap: time.Millisecond, MaxWait: time.Millisecond}})
	}
	job, err := repo.EnqueueRefresh(t.Context(), inventory.RefreshRequest{AccountID: a.ID, ResourceID: "opaque", Operations: []collection.Operation{collection.Detail}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	run, err := (&inventory.Syncer{Repository: repo, Client: factory}).RunOne(t.Context())
	if !axm.IsRateLimited(err) || run.State != "deferred" || run.FinishedAt != nil || run.RetryAt.Before(time.Now().Add(59*time.Minute)) {
		t.Fatalf("defer: %+v %v", run, err)
	}
	otherStore, err := inventorystore.Open(t.Context(), db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	other, err := inventory.New(otherStore)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := other.Job(t.Context(), job.ID)
	if err != nil || saved.State != "deferred" || !saved.RetryAt.Equal(run.RetryAt) {
		t.Fatal("replica lost retry deadline", err, saved)
	}
	worker := &inventory.Syncer{Repository: other, Client: factory}
	if _, err = worker.RunOne(t.Context()); !errors.Is(err, inventory.ErrNotFound) || transport.calls != 1 {
		t.Fatal("replica retried early", err, transport.calls)
	}
	if _, err = other.EnqueueRefresh(t.Context(), inventory.RefreshRequest{AccountID: a.ID, ResourceID: "successor", Operations: []collection.Operation{collection.Coverage}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.RunOne(t.Context()); !errors.Is(err, inventory.ErrNotFound) || transport.calls != 1 {
		t.Fatal("new callback bypassed persisted account cooldown", err, transport.calls)
	}
}
