package axmwebhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
)

// CallbackPath is mounted on the public server; IDs select keys, not authority.
const CallbackPath = "/axm/notifications/"

// Binding is safe local subscription metadata. Revision fences administrative updates.
type Binding struct {
	ID               string    `json:"id"`
	AccountID        string    `json:"account_id"`
	AccountCreatedAt time.Time `json:"account_created_at"`
	AccountRevision  int64     `json:"account_revision"`
	Revision         int64     `json:"revision"`
	RemoteID         string    `json:"remote_id,omitempty"`
	CallbackURL      string    `json:"callback_url"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
	LastReceivedAt   time.Time `json:"last_received_at,omitzero"`
}

type record struct {
	Binding
	Secret         []byte    `json:"secret"`
	PreviousSecret []byte    `json:"previous_secret,omitempty"`
	PreviousUntil  time.Time `json:"previous_until,omitzero"`
}

// Receipt retains exact original bytes independently from inventory observations.
// Hosts must apply raw-data authority before exposing Body.
type Receipt struct {
	ID        string    `json:"id"`
	BindingID string    `json:"binding_id"`
	EventID   string    `json:"event_id"`
	Type      string    `json:"type"`
	Digest    string    `json:"digest"`
	At        time.Time `json:"at"`
	Body      []byte    `json:"body"`
	JobID     string    `json:"job_id,omitempty"`
}

// Coordinator requires a backend whose persistence/encryption contract is supplied
// by the host. The reference app enables it only with encrypted SQL storage.
type Coordinator struct{ Repository *inventory.Repository }

// load decodes a selected inventory document without exposing its contents in errors.
func load[T any](ctx context.Context, r inventory.Reader, key string) (T, error) {
	var out T
	b, err := r.Get(ctx, key)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

// save encodes one document into the caller-owned transaction.
func save(ctx context.Context, tx inventory.Tx, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, key, b)
}

// bindingKey addresses the callback binding document independently of Apple IDs.
func bindingKey(id string) string { return "axm-webhook/binding/" + id }

// hash bounds local receipt keys using the configured binding and provider event identity.
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

// Prepare stores a signing key and provisioning intent before the Apple POST.
// A caller retries by inspecting this ID, never by issuing another create.
func (c *Coordinator) Prepare(ctx context.Context, accountID, publicURL string) (Binding, error) {
	var out Binding
	u, err := httpsurl.Parse(publicURL)
	if err != nil || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return out, inventory.ErrInvalid
	}
	id := rand.Text()
	out = Binding{ID: id, AccountID: accountID, Revision: 1, State: "provisioning", CreatedAt: time.Now().UTC()}
	out.CallbackURL = strings.TrimRight(u.String(), "/") + CallbackPath + url.PathEscape(id)
	err = c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		a, err := load[inventory.Account](ctx, tx, "account/"+accountID)
		if err != nil {
			return err
		}
		if !a.Enabled {
			return inventory.ErrConflict
		}
		out.AccountCreatedAt = a.CreatedAt
		out.AccountRevision = a.Revision
		return save(ctx, tx, bindingKey(id), record{Binding: out, Secret: []byte(rand.Text())})
	})
	return out, err
}

// Binding returns safe local metadata for an explicitly selected account.
func (c *Coordinator) Binding(ctx context.Context, account, id string) (Binding, error) {
	r, err := load[record](ctx, c.Repository.Backend, bindingKey(id))
	if err != nil {
		return Binding{}, err
	}
	if r.AccountID != account {
		return Binding{}, inventory.ErrNotFound
	}
	return r.Binding, nil
}

// Bindings returns a bounded page of safe account-scoped metadata.
func (c *Coordinator) Bindings(ctx context.Context, account, after string) ([]Binding, error) {
	out := []Binding{}
	after = bindingKey(after)
	for {
		rows, err := c.Repository.Backend.Scan(ctx, "axm-webhook/binding/", after, 100)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var r record
			if err := json.Unmarshal(row.Value, &r); err != nil {
				return nil, err
			}
			if r.AccountID == account {
				out = append(out, r.Binding)
				if len(out) == 100 {
					return out, nil
				}
			}
			after = row.Key
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

// SigningSecret is only for an already-authorized provisioning call, never reads.
func (c *Coordinator) SigningSecret(ctx context.Context, b Binding) ([]byte, error) {
	r, err := load[record](ctx, c.Repository.Backend, bindingKey(b.ID))
	if err != nil {
		return nil, err
	}
	if r.AccountID != b.AccountID || r.Revision != b.Revision {
		return nil, inventory.ErrConflict
	}
	return append([]byte(nil), r.Secret...), nil
}

// SetState fences stale credentials/account incarnations before recording remote
// results. State is active, uncertain or disabled. Remote network I/O stays outside
// the transaction. Reconciliation supplies a verified remote ID explicitly.
func (c *Coordinator) SetState(ctx context.Context, b Binding, state, remoteID string) (Binding, error) {
	var out Binding
	if state != "active" && state != "uncertain" && state != "disabled" {
		return out, inventory.ErrInvalid
	}
	if state == "active" && remoteID == "" {
		return out, inventory.ErrInvalid
	}
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		r, err := load[record](ctx, tx, bindingKey(b.ID))
		if err != nil {
			return err
		}
		if r.Revision != b.Revision || r.AccountID != b.AccountID {
			return inventory.ErrConflict
		}
		a, err := load[inventory.Account](ctx, tx, "account/"+r.AccountID)
		if err != nil {
			return err
		}
		if !a.Enabled || a.Revision != b.AccountRevision || !a.CreatedAt.Equal(b.AccountCreatedAt) {
			return inventory.ErrConflict
		}
		r.State = state
		r.RemoteID = remoteID
		r.Revision++
		out = r.Binding
		return save(ctx, tx, bindingKey(b.ID), r)
	})
	return out, err
}

// Receive verifies and commits receipt and work atomically. Repeated identical
// provider identities are harmless; different bytes under an existing ID conflict.
func (c *Coordinator) Receive(ctx context.Context, id string, req *http.Request) (Receipt, error) {
	var out Receipt
	r, err := load[record](ctx, c.Repository.Backend, bindingKey(id))
	if err != nil {
		return out, err
	}
	// A request is bounded/read once. Overlapping keys verify the same original bytes.
	n, err := decode(req, r)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(n.Raw)
	out = Receipt{ID: hash(r.ID + "\x00" + n.Data.ID), BindingID: r.ID, EventID: n.Data.ID, Type: n.Data.Type, Digest: hex.EncodeToString(sum[:]), At: time.Now().UTC(), Body: append([]byte(nil), n.Raw...)}
	err = c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		current, err := load[record](ctx, tx, bindingKey(id))
		if err != nil {
			return err
		}
		if current.Revision != r.Revision || current.State != "active" {
			return inventory.ErrConflict
		}
		a, err := load[inventory.Account](ctx, tx, "account/"+r.AccountID)
		if err != nil {
			return err
		}
		if !a.Enabled || a.Revision != r.AccountRevision || !a.CreatedAt.Equal(r.AccountCreatedAt) {
			return inventory.ErrConflict
		}
		key := "axm-webhook/receipt/" + out.ID
		previous, err := load[Receipt](ctx, tx, key)
		if err == nil {
			if previous.Digest != out.Digest {
				return inventory.ErrConflict
			}
			out = previous
			return nil
		}
		if !errors.Is(err, inventory.ErrNotFound) {
			return err
		}
		var ops []collection.Operation
		if n.Data.Version == 1 {
			ops = refreshOperations(n.Data.Type)
		}
		if len(ops) > 0 {
			resource := n.DeviceResourceID()
			if resource == "" {
				return axm.ErrWebhookNotification
			}
			job, err := c.Repository.EnqueueRefreshTx(ctx, tx, inventory.RefreshRequest{AccountID: r.AccountID, ResourceID: resource, Operations: ops}, out.At)
			if err != nil {
				return err
			}
			out.JobID = job.ID
		}
		if err := save(ctx, tx, key, out); err != nil {
			return err
		}
		current.LastReceivedAt = out.At
		return save(ctx, tx, bindingKey(id), current)
	})
	return out, err
}

// Receipts returns a bounded selected-binding page; raw controls body disclosure.
func (c *Coordinator) Receipts(ctx context.Context, account, id, after string, raw bool) ([]Receipt, error) {
	if _, err := c.Binding(ctx, account, id); err != nil {
		return nil, err
	}
	out := []Receipt{}
	after = "axm-webhook/receipt/" + after
	for {
		rows, err := c.Repository.Backend.Scan(ctx, "axm-webhook/receipt/", after, 100)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var r Receipt
			if err := json.Unmarshal(row.Value, &r); err != nil {
				return nil, err
			}
			if r.BindingID == id {
				if !raw {
					r.Body = nil
					r.EventID = ""
					r.Digest = ""
				}
				out = append(out, r)
				if len(out) == 100 {
					return out, nil
				}
			}
			after = row.Key
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

// refreshOperations maps supported notification types to independent source reads.
func refreshOperations(typ string) []collection.Operation {
	switch typ {
	case axm.NotificationDeviceChanged, axm.NotificationMigrationChanged:
		return []collection.Operation{collection.Detail}
	case axm.NotificationAssignmentChanged:
		return []collection.Operation{collection.Detail, collection.Assignment}
	case axm.NotificationWarrantyChanged:
		return []collection.Operation{collection.Coverage}
	default:
		return nil
	}
}

// Rotate persists a new signing key before an explicit remote update. Failed
// updates remain uncertain and require reconciliation. Overlap is at most a day.
func (c *Coordinator) Rotate(ctx context.Context, b Binding, overlap time.Duration) (Binding, error) {
	var out Binding
	if overlap < 0 || overlap > 24*time.Hour {
		return out, inventory.ErrInvalid
	}
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		r, err := load[record](ctx, tx, bindingKey(b.ID))
		if err != nil {
			return err
		}
		if r.Revision != b.Revision || r.AccountID != b.AccountID {
			return inventory.ErrConflict
		}
		a, err := load[inventory.Account](ctx, tx, "account/"+r.AccountID)
		if err != nil {
			return err
		}
		if a.Revision != b.AccountRevision || !a.CreatedAt.Equal(b.AccountCreatedAt) || !a.Enabled {
			return inventory.ErrConflict
		}
		r.PreviousSecret = r.Secret
		r.PreviousUntil = time.Now().UTC().Add(overlap)
		r.Secret = []byte(rand.Text())
		r.State = "uncertain"
		r.Revision++
		out = r.Binding
		return save(ctx, tx, bindingKey(b.ID), r)
	})
	return out, err
}

// decode verifies bounded original bytes against the current key or unexpired overlap key.
func decode(req *http.Request, r record) (axm.WebhookNotification, error) {
	if req == nil || req.Body == nil {
		return axm.WebhookNotification{}, axm.ErrWebhookNotification
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, axm.MaxWebhookNotificationBytes+1))
	if err != nil {
		return axm.WebhookNotification{}, err
	}
	if len(body) > axm.MaxWebhookNotificationBytes {
		return axm.WebhookNotification{}, axm.ErrWebhookNotification
	}
	attempt := func(secret []byte) (axm.WebhookNotification, error) {
		clone := req.Clone(req.Context())
		clone.Body = io.NopCloser(bytes.NewReader(body))
		return axm.DecodeWebhookNotification(clone, secret)
	}
	n, err := attempt(r.Secret)
	if errors.Is(err, axm.ErrWebhookSignature) && len(r.PreviousSecret) > 0 && time.Now().Before(r.PreviousUntil) {
		return attempt(r.PreviousSecret)
	}
	return n, err
}
