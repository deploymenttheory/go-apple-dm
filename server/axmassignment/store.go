package axmassignment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// Binding authorizes assignment only to this MDM service using the current account revision.
type Binding struct {
	Generation  string                 `json:"generation"`
	AccountID   string                 `json:"account_id"`
	Revision    int64                  `json:"account_revision"`
	Fingerprint string                 `json:"fingerprint"`
	ServiceID   string                 `json:"service_id"`
	Columns     axm.ActivityLogColumns `json:"log_columns"`
}

// Target retains Apple's addressing identity separately from physical correlation.
type Target struct {
	Serial          string     `json:"serial"`
	DeviceID        string     `json:"org_device_id,omitempty"`
	Outcome         string     `json:"outcome,omitempty"`
	ObservedService string     `json:"observed_service,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	Converged       bool       `json:"converged"`
}

// Workflow retains intent, acceptance, terminal outcome and independent observations.
type Workflow struct {
	observations        []inventory.Observation
	Operation           string                `json:"operation"`
	ID                  string                `json:"id"`
	Binding             Binding               `json:"binding"`
	State               string                `json:"state"`
	Targets             []Target              `json:"targets"`
	ActivityID          string                `json:"activity_id,omitempty"`
	SubmissionStartedAt *time.Time            `json:"submission_started_at,omitempty"`
	AcceptedAt          *time.Time            `json:"accepted_at,omitempty"`
	TerminalStatus      axm.ActivityStatus    `json:"terminal_status,omitempty"`
	TerminalSubStatus   axm.ActivitySubStatus `json:"terminal_sub_status,omitempty"`
	LogComplete         bool                  `json:"log_complete"`
	LogSucceeded        bool                  `json:"log_succeeded"`
	Error               string                `json:"error,omitempty"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	NextAttempt         time.Time             `json:"next_attempt"`
}

// Evidence retains exact raw CSV and all parsed fields, including partial failures.
type Evidence struct {
	CSV []byte          `json:"csv"`
	Log axm.ActivityLog `json:"log"`
}

type lease struct {
	Owner string    `json:"owner"`
	Until time.Time `json:"until"`
}

// Controller uses the application's existing inventory repository and outbound client factory.
type Controller struct {
	Repository *inventory.Repository
	Client     func(context.Context, inventory.Account, []byte) (*axm.Client, error)
}

// read decodes a document without altering its source bytes.
func read[T any](ctx context.Context, r inventory.Reader, key string) (T, error) {
	var v T
	b, err := r.Get(ctx, key)
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	return v, err
}

// write persists a document in the caller's atomic transaction.
func write(ctx context.Context, tx inventory.Tx, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, key, b)
}

// fingerprint binds jobs to account incarnation and credentials, including delete/recreate.
func fingerprint(a inventory.Account, key []byte) string {
	// Verification is an observation, not an account configuration replacement.
	a.VerifiedAt = nil
	b, _ := json.Marshal(a)
	h := sha256.New()
	_, _ = h.Write(b)
	_, _ = h.Write(key)
	return hex.EncodeToString(h.Sum(nil))
}

// snapshot reads account and key together under the same backend transaction.
func snapshot(ctx context.Context, tx inventory.Reader, id string) (inventory.Account, []byte, error) {
	a, e := read[inventory.Account](ctx, tx, "account/"+id)
	if e != nil {
		return a, nil, e
	}
	if !a.Enabled {
		return a, nil, inventory.ErrStopped
	}
	key, e := read[[]byte](ctx, tx, "credential/"+id)
	return a, key, e
}

// Configure explicitly binds the named account's current revision to this MDM.
// The service is checked by the worker before mutation; configuration makes no Apple calls.
func (c *Controller) Configure(ctx context.Context, account string, revision int64, service string, columns axm.ActivityLogColumns) (Binding, error) {
	var out Binding
	if strings.TrimSpace(service) == "" || columns.Serial == "" || columns.Outcome == "" || columns.Success == "" || columns.Serial == columns.Outcome {
		return out, inventory.ErrInvalid
	}
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		a, key, e := snapshot(ctx, tx, account)
		if e != nil {
			return e
		}
		if a.Revision != revision {
			return inventory.ErrConflict
		}
		out = Binding{Generation: inventory.ID(), AccountID: a.ID, Revision: a.Revision, Fingerprint: fingerprint(a, key), ServiceID: service, Columns: columns}
		return write(ctx, tx, "assignment-binding/"+a.ID, out)
	})
	return out, err
}

// Binding returns the explicitly configured service, which may need rebinding after rotation.
func (c *Controller) Binding(ctx context.Context, account string) (Binding, error) {
	return read[Binding](ctx, c.Repository.Backend, "assignment-binding/"+account)
}

// Request persists one explicit assignment. A caller-provided request ID makes retries idempotent.
// Outstanding overlapping targets are rejected even when the earlier submission is uncertain.
func (c *Controller) Request(ctx context.Context, account, id string, serials []string, now time.Time) (Workflow, error) {
	return c.request(ctx, account, id, serials, "assign", now)
}

// RequestUnassign removes devices only from the configured service after a checked readback.
func (c *Controller) RequestUnassign(ctx context.Context, account, id string, serials []string, now time.Time) (Workflow, error) {
	return c.request(ctx, account, id, serials, "unassign", now)
}

// request atomically deduplicates and persists the explicit operation.
func (c *Controller) request(ctx context.Context, account, id string, serials []string, operation string, now time.Time) (Workflow, error) {
	var out Workflow
	if id == "" || len(id) > 128 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" || len(serials) == 0 || len(serials) > 1000 {
		return out, inventory.ErrInvalid
	}
	seen := map[string]bool{}
	for _, s := range serials {
		if strings.TrimSpace(s) == "" || seen[s] {
			return out, inventory.ErrInvalid
		}
		seen[s] = true
	}
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		a, key, e := snapshot(ctx, tx, account)
		if e != nil {
			return e
		}
		b, e := read[Binding](ctx, tx, "assignment-binding/"+account)
		if e != nil {
			return e
		}
		if b.Revision != a.Revision || b.Fingerprint != fingerprint(a, key) {
			return inventory.ErrConflict
		}
		old, e := read[Workflow](ctx, tx, "assignment/"+id)
		if e == nil {
			if old.Operation != operation || old.Binding != b || len(old.Targets) != len(serials) {
				return inventory.ErrConflict
			}
			for i, s := range serials {
				if old.Targets[i].Serial != s {
					return inventory.ErrConflict
				}
			}
			out = old
			return nil
		}
		if !errors.Is(e, inventory.ErrNotFound) {
			return e
		}
		after := ""
		for {
			rows, e := tx.Scan(ctx, "assignment/", after, 128)
			if e != nil {
				return e
			}
			for _, row := range rows {
				var w Workflow
				if e = json.Unmarshal(row.Value, &w); e != nil {
					return e
				}
				// Fencing revokes local authority; it does not resolve a possibly accepted remote mutation.
				if w.Binding.AccountID == account && w.State != "complete" && (w.State != "fenced" || w.SubmissionStartedAt != nil) {
					for _, target := range w.Targets {
						if seen[target.Serial] {
							return inventory.ErrConflict
						}
					}
				}
				after = row.Key
			}
			if len(rows) < 128 {
				break
			}
		}
		out = Workflow{ID: id, Operation: operation, Binding: b, State: "queued", CreatedAt: now, UpdatedAt: now, NextAttempt: now}
		for _, s := range serials {
			out.Targets = append(out.Targets, Target{Serial: s})
		}
		return write(ctx, tx, "assignment/"+id, out)
	})
	return out, err
}

// Get returns public workflow evidence only for its named account.
func (c *Controller) Get(ctx context.Context, account, id string) (Workflow, error) {
	w, e := read[Workflow](ctx, c.Repository.Backend, "assignment/"+id)
	if e == nil && w.Binding.AccountID != account {
		e = inventory.ErrNotFound
	}
	return w, e
}

// Evidence returns sensitive retained CSV independently of ordinary workflow reads.
func (c *Controller) Evidence(ctx context.Context, account, id string) (Evidence, error) {
	if _, e := c.Get(ctx, account, id); e != nil {
		return Evidence{}, e
	}
	w, e := c.Get(ctx, account, id)
	if e != nil {
		return Evidence{}, e
	}
	count, e := read[int](ctx, c.Repository.Backend, "assignment-evidence/"+id)
	if e != nil {
		return Evidence{}, e
	}
	var raw []byte
	for i := 0; i < count; i++ {
		part, e := read[[]byte](ctx, c.Repository.Backend, "assignment-log/"+id+"/"+strconv.Itoa(i))
		if e != nil {
			return Evidence{}, e
		}
		raw = append(raw, part...)
	}
	serials := make([]string, len(w.Targets))
	for i, t := range w.Targets {
		serials[i] = t.Serial
	}
	log, _ := axm.VerifyActivityLog(bytes.NewReader(raw), w.Binding.Columns, serials)
	return Evidence{CSV: raw, Log: log}, nil
}
