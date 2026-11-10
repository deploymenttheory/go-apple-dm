package axmwebhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// fixture creates an active account and binding in isolated transactional storage.
func fixture(t *testing.T) (*Coordinator, Binding, []byte) {
	t.Helper()
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	account := inventory.Account{ID: "account", Enabled: true, Revision: 1, CreatedAt: time.Now().UTC()}
	if err := repo.Backend.Update(t.Context(), func(tx inventory.Tx) error { return save(t.Context(), tx, "account/account", account) }); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Repository: repo}
	b, err := c.Prepare(t.Context(), "account", "https://mdm.example")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := c.SigningSecret(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	b, err = c.SetState(t.Context(), b, "active", "apple-subscription")
	if err != nil {
		t.Fatal(err)
	}
	return c, b, secret
}

// signed creates an independently signed callback request with the supplied context.
func signed(ctx context.Context, body string, secret []byte) *http.Request {
	r := httptest.NewRequestWithContext(ctx, "POST", "https://mdm.example/callback", strings.NewReader(body))
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(body))
	r.Header.Set("x-apple-signature", "hmacsha256="+hex.EncodeToString(m.Sum(nil)))
	return r
}

// eventBody builds a fictional device callback with distinct serial and opaque IDs.
func eventBody(id, typ string) string {
	return `{"data":{"id":"` + id + `","type":"` + typ + `","version":1,"attributes":{"orgDeviceSerialNumber":"SER"},"relationships":{"instance":{"data":{"type":"orgDevices","id":"opaque"}}}}}`
}

// TestReceiptAndWorkAtomic checks notification mapping, deduplication and safe receipt inspection.
func TestReceiptAndWorkAtomic(t *testing.T) {
	for typ, want := range map[string][]collection.Operation{
		axm.NotificationDeviceChanged:     {collection.Detail},
		axm.NotificationMigrationChanged:  {collection.Detail},
		axm.NotificationAssignmentChanged: {collection.Detail, collection.Assignment},
		axm.NotificationWarrantyChanged:   {collection.Coverage},
		axm.NotificationPing:              nil,
		"future":                          nil,
	} {
		t.Run(typ, func(t *testing.T) {
			c, b, secret := fixture(t)
			body := eventBody("event", typ)
			receipt, err := c.Receive(t.Context(), b.ID, signed(t.Context(), body, secret))
			if err != nil {
				t.Fatal(err)
			}
			if string(receipt.Body) != body {
				t.Fatal("lost original body")
			}
			again, err := c.Receive(t.Context(), b.ID, signed(t.Context(), body, secret))
			if err != nil || again.ID != receipt.ID {
				t.Fatal("duplicate was not idempotent", err)
			}
			if len(want) == 0 {
				if receipt.JobID != "" {
					t.Fatal("unexpected refresh")
				}
			} else {
				job, err := c.Repository.Job(t.Context(), receipt.JobID)
				if err != nil || job.ResourceID != "opaque" || !slices.Equal(job.Operations, want) {
					t.Fatal("wrong job", job, err)
				}
			}
			if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), body+" ", secret)); !errors.Is(err, inventory.ErrConflict) {
				t.Fatal("conflicting identity accepted", err)
			}
			rows, err := c.Receipts(t.Context(), b.AccountID, b.ID, "", false)
			if err != nil || len(rows) != 1 || rows[0].Body != nil || rows[0].EventID != "" {
				t.Fatal("metadata leaked raw fields", err)
			}
			rows, err = c.Receipts(t.Context(), b.AccountID, b.ID, "", true)
			if err != nil || len(rows) != 1 || string(rows[0].Body) != body {
				t.Fatal("raw receipt lost", err)
			}
			if _, err := c.Binding(t.Context(), "other", b.ID); !errors.Is(err, inventory.ErrNotFound) {
				t.Fatal("cross-account lookup", err)
			}
		})
	}
}

type failingBackend struct {
	inventory.Backend
	fail bool
}

// Update joins the supplied transaction callback while injecting the configured failure.
func (f *failingBackend) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	return f.Backend.Update(ctx, func(tx inventory.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if f.fail {
			return errors.New("commit failure")
		}
		return nil
	})
}

// TestRollbackRestartAndFencing checks receipt/work rollback and account incarnation isolation.
func TestRollbackRestartAndFencing(t *testing.T) {
	c, b, secret := fixture(t)
	backend := &failingBackend{Backend: c.Repository.Backend, fail: true}
	c.Repository.Backend = backend
	body := eventBody("event", axm.NotificationDeviceChanged)
	if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), body, secret)); err == nil {
		t.Fatal("commit failure acknowledged")
	}
	rows, err := c.Repository.Jobs(t.Context(), "", 100)
	if err != nil || len(rows) != 0 {
		t.Fatal("work survived rollback", err)
	}
	backend.fail = false
	restarted := &Coordinator{Repository: c.Repository}
	if _, err := restarted.Receive(t.Context(), b.ID, signed(t.Context(), body, secret)); err != nil {
		t.Fatal(err)
	}
	if err := c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
		a, err := load[inventory.Account](t.Context(), tx, "account/account")
		if err != nil {
			return err
		}
		a.CreatedAt = a.CreatedAt.Add(time.Second)
		return save(t.Context(), tx, "account/account", a)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), eventBody("next", axm.NotificationDeviceChanged), secret)); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("recreated account accepted", err)
	}
}

// TestRotationAndDisabledBinding checks key overlap, stale revisions and disabled bindings.
func TestRotationAndDisabledBinding(t *testing.T) {
	c, b, old := fixture(t)
	rotated, err := c.Rotate(t.Context(), b, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.SigningSecret(t.Context(), rotated)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err = c.SetState(t.Context(), rotated, "active", b.RemoteID)
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range [][]byte{old, next} {
		if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), eventBody(string(rune('a'+i)), axm.NotificationPing), key)); err != nil {
			t.Fatal("overlap failed", err)
		}
	}
	if _, err := c.SetState(t.Context(), b, "disabled", b.RemoteID); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	disabled, err := c.SetState(t.Context(), rotated, "disabled", b.RemoteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Receive(t.Context(), disabled.ID, signed(t.Context(), eventBody("disabled", axm.NotificationPing), next)); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("disabled accepted", err)
	}
	if _, err := c.Rotate(t.Context(), disabled, -time.Second); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.SetState(t.Context(), disabled, "invalid", ""); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.Prepare(t.Context(), "account", "http://insecure"); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	raw, err := json.Marshal(disabled)
	if err != nil || strings.Contains(string(raw), string(next)) {
		t.Fatal("secret in binding metadata")
	}
}

// faultBackend exercises public storage-failure contracts without changing state.
type faultBackend struct {
	inventory.Backend
	getErr, scanErr, putErr bool
	badKey                  string
}

// Get injects selected read failures or delegates to retained test state.
func (f faultBackend) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if f.getErr {
		return nil, errors.New("read unavailable")
	}
	if key == f.badKey {
		return json.RawMessage(`[]`), nil
	}
	return f.Backend.Get(ctx, key)
}

// Scan injects enumeration failure without changing retained state.
func (f faultBackend) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	if f.scanErr {
		return nil, errors.New("scan unavailable")
	}
	return f.Backend.Scan(ctx, prefix, after, limit)
}

// Update joins the supplied transaction callback while injecting the configured failure.
func (f faultBackend) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	return f.Backend.Update(ctx, func(tx inventory.Tx) error { return fn(faultTx{Tx: tx, putErr: f.putErr, badKey: f.badKey}) })
}

type faultTx struct {
	inventory.Tx
	putErr bool
	badKey string
}

// Get injects selected read failures or delegates to retained test state.
func (f faultTx) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if key == f.badKey {
		return json.RawMessage(`[]`), nil
	}
	return f.Tx.Get(ctx, key)
}

// Put injects transaction write failure without retaining partial work.
func (f faultTx) Put(ctx context.Context, key string, v json.RawMessage) error {
	if f.putErr {
		return errors.New("write unavailable")
	}
	return f.Tx.Put(ctx, key, v)
}

// TestCoordinatorStorageFailures checks that failed storage operations preserve binding and receipt state.
func TestCoordinatorStorageFailures(t *testing.T) {
	for _, name := range []string{"prepare-write", "binding-read", "bindings-scan", "secret-read", "state-write", "rotate-write", "receive-write", "receipts-scan", "prepare-account", "state-account", "rotate-account", "receive-account"} {
		t.Run(name, func(t *testing.T) {
			c, b, secret := fixture(t)
			original := c.Repository.Backend
			fault := faultBackend{Backend: original, getErr: strings.HasSuffix(name, "-read"), scanErr: strings.HasSuffix(name, "-scan"), putErr: strings.HasSuffix(name, "-write")}
			if strings.HasSuffix(name, "-account") {
				fault.badKey = "account/account"
			}
			c.Repository.Backend = fault
			var err error
			switch strings.Split(name, "-")[0] {
			case "prepare":
				_, err = c.Prepare(t.Context(), "account", "https://mdm.example")
			case "binding":
				_, err = c.Binding(t.Context(), b.AccountID, b.ID)
			case "bindings":
				_, err = c.Bindings(t.Context(), b.AccountID, "")
			case "secret":
				_, err = c.SigningSecret(t.Context(), b)
			case "state":
				_, err = c.SetState(t.Context(), b, "disabled", b.RemoteID)
			case "rotate":
				_, err = c.Rotate(t.Context(), b, time.Hour)
			case "receive":
				_, err = c.Receive(t.Context(), b.ID, signed(t.Context(), eventBody("event", axm.NotificationPing), secret))
			case "receipts":
				_, err = c.Receipts(t.Context(), b.AccountID, b.ID, "", true)
			}
			if err == nil {
				t.Fatal("storage failure was acknowledged")
			}
			c.Repository.Backend = original
			actual, err := c.Binding(t.Context(), b.AccountID, b.ID)
			if err != nil || actual.Revision != b.Revision {
				t.Fatal("failed write changed binding", err)
			}
			rows, err := c.Receipts(t.Context(), b.AccountID, b.ID, "", true)
			if err != nil || len(rows) != 0 {
				t.Fatal("receipt survived failed write", err)
			}
		})
	}
}

// TestRejectedCallbacksAndAdministrativeArguments rejects malformed callbacks and unsafe administrative input.
func TestRejectedCallbacksAndAdministrativeArguments(t *testing.T) {
	c, b, secret := fixture(t)
	if _, err := c.SigningSecret(t.Context(), Binding{ID: b.ID, AccountID: "other", Revision: b.Revision}); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := c.SetState(t.Context(), b, "active", ""); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.Prepare(t.Context(), "missing", "https://mdm.example"); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal(err)
	}
	for _, r := range []*http.Request{nil, signed(t.Context(), "invalid", secret), signed(t.Context(), eventBody("bad", axm.NotificationPing), []byte("wrong")), signed(t.Context(), strings.Repeat("x", axm.MaxWebhookNotificationBytes+1), secret)} {
		if _, err := c.Receive(t.Context(), b.ID, r); err == nil {
			t.Fatal("invalid callback accepted")
		}
	}
	unknown := strings.Replace(eventBody("future", axm.NotificationDeviceChanged), `"version":1`, `"version":2`, 1)
	receipt, err := c.Receive(t.Context(), b.ID, signed(t.Context(), unknown, secret))
	if err != nil || receipt.JobID != "" {
		t.Fatal("unknown schema executed", err)
	}
	missing := `{"data":{"id":"missing","type":"orgDeviceAttributeChanged","version":1}}`
	if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), missing, secret)); !errors.Is(err, axm.ErrWebhookNotification) {
		t.Fatal("missing resource linkage accepted", err)
	}
}

// TestScopedPagination checks matching records beyond another account's page.
func TestScopedPagination(t *testing.T) {
	c, b, _ := fixture(t)
	if err := c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
		for i := range 102 {
			other := record{Binding: Binding{ID: fmt.Sprintf("000-other-%03d", i), AccountID: "other"}}
			if err := save(t.Context(), tx, bindingKey(other.ID), other); err != nil {
				return err
			}
			if err := save(t.Context(), tx, "axm-webhook/receipt/"+other.ID, Receipt{ID: other.ID, BindingID: "other"}); err != nil {
				return err
			}
		}
		for i := range 101 {
			owned := record{Binding: Binding{ID: fmt.Sprintf("zzz-owned-%03d", i), AccountID: b.AccountID}}
			if err := save(t.Context(), tx, bindingKey(owned.ID), owned); err != nil {
				return err
			}
			if err := save(t.Context(), tx, "axm-webhook/receipt/"+owned.ID, Receipt{ID: owned.ID, BindingID: b.ID, Body: []byte("original")}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := c.Bindings(t.Context(), b.AccountID, "")
	if err != nil || len(first) != 100 {
		t.Fatal(len(first), err)
	}
	second, err := c.Bindings(t.Context(), b.AccountID, first[99].ID)
	if err != nil || len(second) != 2 {
		t.Fatal(len(second), err)
	}
	receipts, err := c.Receipts(t.Context(), b.AccountID, b.ID, "", false)
	if err != nil || len(receipts) != 100 {
		t.Fatal(len(receipts), err)
	}
	remaining, err := c.Receipts(t.Context(), b.AccountID, b.ID, receipts[99].ID, true)
	if err != nil || len(remaining) != 1 || string(remaining[0].Body) != "original" {
		t.Fatal(remaining, err)
	}
	if err := c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
		if err := tx.Put(t.Context(), bindingKey("000-corrupt"), json.RawMessage(`[]`)); err != nil {
			return err
		}
		return tx.Put(t.Context(), "axm-webhook/receipt/000-corrupt", json.RawMessage(`[]`))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Bindings(t.Context(), b.AccountID, ""); err == nil {
		t.Fatal("corrupt binding acknowledged")
	}
	if _, err := c.Receipts(t.Context(), b.AccountID, b.ID, "", true); err == nil {
		t.Fatal("corrupt receipt acknowledged")
	}
}

// TestMissingAndDisabledAccounts checks administrative and receive fences.
func TestMissingAndDisabledAccounts(t *testing.T) {
	c, b, secret := fixture(t)
	missing := b
	missing.ID = "missing"
	if _, err := c.SetState(t.Context(), missing, "disabled", b.RemoteID); err == nil {
		t.Fatal("missing binding updated")
	}
	if _, err := c.Rotate(t.Context(), missing, time.Hour); err == nil {
		t.Fatal("missing binding rotated")
	}
	if _, err := c.SigningSecret(t.Context(), missing); err == nil {
		t.Fatal("missing key returned")
	}
	if _, err := c.Receive(t.Context(), missing.ID, signed(t.Context(), eventBody("event", axm.NotificationPing), secret)); err == nil {
		t.Fatal("missing binding accepted")
	}
	if _, err := c.Receipts(t.Context(), b.AccountID, missing.ID, "", true); err == nil {
		t.Fatal("missing binding receipts returned")
	}
	if err := c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
		a, err := load[inventory.Account](t.Context(), tx, "account/account")
		if err != nil {
			return err
		}
		a.Enabled = false
		return save(t.Context(), tx, "account/account", a)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Prepare(t.Context(), b.AccountID, "https://mdm.example"); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := c.SetState(t.Context(), b, "disabled", b.RemoteID); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := c.Rotate(t.Context(), b, time.Hour); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
}
