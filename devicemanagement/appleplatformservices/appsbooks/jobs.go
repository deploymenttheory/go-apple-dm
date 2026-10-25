package appsbooks

import (
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var (
	// ErrJobBusy indicates a location has an unfinished submission or live worker.
	ErrJobBusy = errors.New("appsbooks: location worker busy")
	// ErrJobFence rejects a superseded or expired worker's publication.
	ErrJobFence = errors.New("appsbooks: stale job worker")
	// ErrReconciliation requires explicit investigation, never automatic POST replay.
	ErrReconciliation = errors.New("appsbooks: submission requires reconciliation")
	// ErrPollEarly enforces Apple's minimum 30 second pending polling interval.
	ErrPollEarly = errors.New("appsbooks: event polling is not yet due")
)

// JobOperation selects one explicit asynchronous Apple operation.
type JobOperation string

const (
	JobAssociate    JobOperation = "ASSOCIATE"
	JobDisassociate JobOperation = "DISASSOCIATE"
	JobRevoke       JobOperation = "REVOKE"
	JobCreateUsers  JobOperation = "CREATE"
	JobUpdateUsers  JobOperation = "UPDATE"
	JobRetireUsers  JobOperation = "RETIRE"
)

// JobRequest contains exactly one request body. MaxPages bounds fresh capability
// and active-user preflight reads before submission. User IDs are caller supplied;
// no physical serial is converted into an Apple user identity.
type JobRequest struct {
	Operation JobOperation         `json:"operation"`
	Assets    *ManageAssetsRequest `json:"assets,omitempty"`
	Revoke    *RevokeAssetsRequest `json:"revoke,omitempty"`
	Users     *ManageUsersRequest  `json:"users,omitempty"`
	MaxPages  int                  `json:"maxPages"`
}

// JobState distinguishes intent, remote uncertainty and confirmed completion.
type JobState string

const (
	JobQueued     JobState = "queued"
	JobSubmitting JobState = "submitting"
	JobUncertain  JobState = "uncertain"
	JobAccepted   JobState = "accepted"
	JobSucceeded  JobState = "succeeded"
	JobFailed     JobState = "failed"
	JobAbandoned  JobState = "abandoned"
)

// TargetResult is notification evidence for one requested asset/target or user.
// Multiple callback identities never duplicate this result. A batch observation
// does not establish event completion; complete original callbacks remain retained.
type TargetResult struct {
	Assignment *Assignment `json:"assignment,omitempty"`
	User       *User       `json:"user,omitempty"`
	Result     string      `json:"result"`
}

// LicensingJob contains private request/outcome data. Protect its access separately
// from safe Location metadata. Event and status preserve original Apple JSON.
// Worker and Fence identify a lease, not a remote idempotency key.
type LicensingJob struct {
	Preflight           LocationEvidence        `json:"preflight"`
	ID                  string                  `json:"id"`
	LocationID          string                  `json:"locationId"`
	LocationRevision    uint64                  `json:"locationRevision"`
	Request             JobRequest              `json:"request"`
	State               JobState                `json:"state"`
	EventID             string                  `json:"eventId"`
	EventRaw            []byte                  `json:"eventRaw"`
	StatusRaw           []byte                  `json:"statusRaw"`
	Status              *Status                 `json:"status,omitempty"`
	Targets             map[string]TargetResult `json:"targets"`
	NotificationIDs     []string                `json:"notificationIds"`
	NotificationFailure bool                    `json:"notificationFailure"`
	Worker              string                  `json:"worker"`
	Fence               uint64                  `json:"fence"`
	LeaseUntil          time.Time               `json:"leaseUntil"`
	NextPollAt          time.Time               `json:"nextPollAt"`
	CreatedAt           time.Time               `json:"createdAt"`
	UpdatedAt           time.Time               `json:"updatedAt"`
	ReconciliationNote  string                  `json:"reconciliationNote"`
}

type locationWorker struct {
	JobID string    `json:"jobId"`
	Fence uint64    `json:"fence"`
	Until time.Time `json:"until"`
}

// jobKey bounds caller-chosen job identifiers inside the location namespace.
func jobKey(location, id string) string {
	return locationKey(location, fmt.Sprintf("jobs/%x", sha256.Sum256([]byte(id))))
}

// eventKey scopes each opaque Apple event identifier to its location.
func eventKey(location, id string) string {
	return locationKey(location, fmt.Sprintf("events/%x", sha256.Sum256([]byte(id))))
}

// licensingBarrier is called under the location metadata lock by every location
// writer. Even an expired submission remains blocked until explicit reconciliation.
func licensingBarrier(ctx context.Context, r state.Reader, id string) error {
	var lock locationWorker
	err := readState(ctx, r, locationKey(id, "worker"), &lock)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrJobBusy
}

// validate rejects ambiguous bodies and malformed explicit target identities.
func (r JobRequest) validate() error {
	if r.MaxPages <= 0 {
		return ErrInput
	}
	switch r.Operation {
	case JobAssociate, JobDisassociate:
		if r.Assets == nil || r.Revoke != nil || r.Users != nil || len(r.Assets.Assets) == 0 || len(r.Assets.ClientUserIDs)+len(r.Assets.SerialNumbers) == 0 || !unique(r.Assets.ClientUserIDs) || !unique(r.Assets.SerialNumbers) {
			return ErrInput
		}
		seen := map[Asset]bool{}
		for _, a := range r.Assets.Assets {
			if a.AdamID == "" || (a.PricingParam != "STDQ" && a.PricingParam != "PLUS") || seen[a] {
				return ErrInput
			}
			seen[a] = true
		}
	case JobRevoke:
		if r.Revoke == nil || r.Assets != nil || r.Users != nil || len(r.Revoke.ClientUserIDs)+len(r.Revoke.SerialNumbers) == 0 || !unique(r.Revoke.ClientUserIDs) || !unique(r.Revoke.SerialNumbers) {
			return ErrInput
		}
	case JobCreateUsers, JobUpdateUsers, JobRetireUsers:
		if r.Users == nil || r.Assets != nil || r.Revoke != nil || len(r.Users.Users) == 0 {
			return ErrInput
		}
		ids := []string{}
		for _, u := range r.Users.Users {
			ids = append(ids, u.ClientUserID)
		}
		if !unique(ids) {
			return ErrInput
		}
	default:
		return ErrInput
	}
	return nil
}

// CreateJob persists explicit intent at an expected location revision, without
// Apple requests. Reusing an ID is idempotent only for the identical request and
// revision. Records are immortal; applications own scheduling and retention.
func (w *Workflow) CreateJob(ctx context.Context, location string, revision uint64, id string, request JobRequest) (LicensingJob, error) {
	if id == "" {
		return LicensingJob{}, ErrInput
	}
	if err := request.validate(); err != nil {
		return LicensingJob{}, err
	}
	var result LicensingJob
	err := w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		l, err := w.readLocation(ctx, tx, location)
		if err != nil {
			return err
		}
		var old LicensingJob
		err = readState(ctx, tx, jobKey(location, id), &old)
		if err == nil {
			if old.LocationRevision != revision || !reflect.DeepEqual(old.Request, request) {
				return ErrInput
			}
			result = old
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if l.Revision != revision {
			return ErrRevision
		}
		if l.ConfigurationPending {
			return ErrConfigurationPending
		}
		result = LicensingJob{ID: id, LocationID: location, LocationRevision: revision, Request: request, State: JobQueued, Targets: map[string]TargetResult{}, CreatedAt: tx.Now(), UpdatedAt: tx.Now()}
		return putState(ctx, tx, jobKey(location, id), result)
	})
	return result, err
}

// LoadJob returns privileged durable intent, lease and observed outcomes.
func (w *Workflow) LoadJob(ctx context.Context, location, id string) (LicensingJob, error) {
	if _, err := w.Load(ctx, location); err != nil {
		return LicensingJob{}, err
	}
	var out LicensingJob
	err := readState(ctx, w.store, jobKey(location, id), &out)
	return out, err
}

// Jobs lists private jobs using an opaque state-key cursor. It makes no Apple
// requests. Pass the last returned record key as after; empty starts traversal.
func (w *Workflow) Jobs(ctx context.Context, location, after string, limit int) ([]LicensingJob, string, error) {
	if _, err := w.Load(ctx, location); err != nil {
		return nil, "", err
	}
	records, err := w.store.List(ctx, locationKey(location, "jobs/"), after, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]LicensingJob, 0, len(records))
	cursor := after
	for _, r := range records {
		var j LicensingJob
		if json.Unmarshal(r.Value, &j) != nil {
			return nil, "", ErrProtocol
		}
		out = append(out, j)
		cursor = r.Key
	}
	return out, cursor, nil
}

// reserveJob serializes HTTP work per location. A submission is irrevocably marked
// before HTTP; a crash therefore cannot make a POST automatically eligible again.
func (w *Workflow) reserveJob(ctx context.Context, location, id, worker string, ttl time.Duration, submit bool) (LicensingJob, error) {
	if worker == "" || ttl <= 0 || ttl > time.Hour {
		return LicensingJob{}, ErrInput
	}
	var out LicensingJob
	err := w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		l, err := w.readLocation(ctx, tx, location)
		if err != nil {
			return err
		}
		if l.ConfigurationPending {
			return ErrConfigurationPending
		}
		if err := readState(ctx, tx, jobKey(location, id), &out); err != nil {
			return err
		}
		var active locationWorker
		err = readState(ctx, tx, locationKey(location, "worker"), &active)
		if err == nil {
			return ErrJobBusy
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if out.Fence == math.MaxUint64 {
			return ErrJobFence
		}
		if submit {
			if out.State != JobQueued {
				return ErrReconciliation
			}
			if out.LocationRevision != l.Revision {
				return ErrRevision
			}
			out.State = JobSubmitting
		} else {
			if out.State != JobAccepted {
				return ErrReconciliation
			}
			if tx.Now().Before(out.NextPollAt) {
				return ErrPollEarly
			}
		}
		out.Fence++
		out.Worker = worker
		out.LeaseUntil = tx.Now().Add(ttl)
		out.UpdatedAt = tx.Now()
		if err := putState(ctx, tx, jobKey(location, id), out); err != nil {
			return err
		}
		return putState(ctx, tx, locationKey(location, "worker"), locationWorker{JobID: id, Fence: out.Fence, Until: out.LeaseUntil})
	})
	return out, err
}

// finishJob fences stale worker publications and atomically releases the location.
func (w *Workflow) finishJob(ctx context.Context, j LicensingJob, fn func(state.Tx, *LicensingJob) error) (LicensingJob, error) {
	var out LicensingJob
	err := w.store.Update(ctx, []string{locationKey(j.LocationID, "metadata")}, func(tx state.Tx) error {
		var active locationWorker
		if err := readState(ctx, tx, locationKey(j.LocationID, "worker"), &active); err != nil {
			return err
		}
		if err := readState(ctx, tx, jobKey(j.LocationID, j.ID), &out); err != nil {
			return err
		}
		if active.JobID != j.ID || active.Fence != j.Fence || out.Fence != j.Fence || out.Worker != j.Worker || !tx.Now().Before(active.Until) {
			return ErrJobFence
		}
		if err := fn(tx, &out); err != nil {
			return err
		}
		out.Worker = ""
		out.LeaseUntil = time.Time{}
		out.UpdatedAt = tx.Now()
		if out.State == JobUncertain {
			out.LeaseUntil = tx.Now()
			if err := putState(ctx, tx, jobKey(j.LocationID, j.ID), out); err != nil {
				return err
			}
			return putState(ctx, tx, locationKey(j.LocationID, "worker"), locationWorker{JobID: j.ID, Fence: j.Fence, Until: tx.Now()})
		}
		if err := putState(ctx, tx, jobKey(j.LocationID, j.ID), out); err != nil {
			return err
		}
		return tx.Delete(ctx, locationKey(j.LocationID, "worker"))
	})
	return out, err
}

// SubmitJob attempts a queued request once. Every failure after reservation leaves
// uncertain intent (or the submitting crash marker), requiring explicit recovery.
// Its return never proves installation. No Apple call runs in a transaction.
func (w *Workflow) SubmitJob(ctx context.Context, location, id, worker string, ttl time.Duration) (LicensingJob, error) {
	j, err := w.reserveJob(ctx, location, id, worker, ttl, true)
	if err != nil {
		return j, err
	}
	_, c, err := w.session(ctx, location, j.LocationRevision, false)
	var event Event
	var preflight LocationEvidence
	if err == nil {
		preflight, err = w.preflight(ctx, c, j.Request)
	}
	if err == nil {
		switch j.Request.Operation {
		case JobAssociate:
			event, err = c.Associate(ctx, *j.Request.Assets)
		case JobDisassociate:
			event, err = c.Disassociate(ctx, *j.Request.Assets)
		case JobRevoke:
			event, err = c.Revoke(ctx, *j.Request.Revoke)
		case JobCreateUsers:
			event, err = c.CreateUsers(ctx, *j.Request.Users)
		case JobUpdateUsers:
			event, err = c.UpdateUsers(ctx, *j.Request.Users)
		case JobRetireUsers:
			event, err = c.RetireUsers(ctx, *j.Request.Users)
		}
	}
	out, persistErr := w.finishJob(ctx, j, func(tx state.Tx, out *LicensingJob) error {
		out.Preflight = preflight
		if err == nil {
			out.EventRaw = event.Raw
			return w.bindEvent(ctx, tx, out, event.EventID)
		}
		// The transaction succeeds to retain uncertainty; SubmitJob returns err.
		out.State = JobUncertain
		return nil
	})
	if persistErr != nil {
		return j, persistErr
	}
	return out, err
}

// preflight uses fresh native source reads, never an inventory projection. A user
// with historical retired rows and one active row remains a valid active target.
func (w *Workflow) preflight(ctx context.Context, c *Client, r JobRequest) (LocationEvidence, error) {
	var evidence LocationEvidence
	config, err := c.ClientConfig(ctx)
	if err != nil {
		return evidence, err
	}
	evidence.Configuration = config.Raw
	var users []string
	if r.Assets != nil {
		batch, err := collectPages(ctx, r.MaxPages, "", func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
			p, err := c.Assets(ctx, AssetsQuery{PageIndex: index})
			return p.Raw, p.Pagination, err
		})
		if err != nil {
			return evidence, err
		}
		evidence.Assets = batch
		found := map[Asset]bool{}
		check := func(asset Asset, validate func(bool, bool) error) error {
			for _, want := range r.Assets.Assets {
				if want == asset {
					if err := validate(len(r.Assets.SerialNumbers) > 0, r.Operation == JobDisassociate); err != nil {
						return err
					}
					found[asset] = true
				}
			}
			return nil
		}
		for _, raw := range batch.Pages {
			var p AssetsPage
			if err := json.Unmarshal(raw, &p); err != nil {
				return evidence, ErrProtocol
			}
			for _, a := range p.Assets {
				if err := check(a.Asset, a.CheckAssignment); err != nil {
					return evidence, err
				}
			}
			for _, a := range p.UnlimitedAssets {
				if err := check(a.Asset, a.CheckAssignment); err != nil {
					return evidence, err
				}
			}
		}
		if len(found) != len(r.Assets.Assets) {
			return evidence, ErrInput
		}
		users = r.Assets.ClientUserIDs
	}
	if r.Revoke != nil {
		users = r.Revoke.ClientUserIDs
	}
	if r.Users != nil && r.Operation != JobCreateUsers {
		for _, u := range r.Users.Users {
			users = append(users, u.ClientUserID)
		}
	}
	if len(users) > 0 {
		batch, err := collectPages(ctx, r.MaxPages, "", func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
			p, err := c.Users(ctx, UsersQuery{PageIndex: index, ActiveOnly: new(true)})
			return p.Raw, p.Pagination, err
		})
		if err != nil {
			return evidence, err
		}
		evidence.Users = []SourceBatch{batch}
		active := map[string]bool{}
		for _, raw := range batch.Pages {
			var p UsersPage
			if json.Unmarshal(raw, &p) != nil {
				return evidence, ErrProtocol
			}
			for _, u := range p.Users {
				if u.Status == "Registered" || u.Status == "Associated" || u.Status == "Created" {
					active[u.ClientUserID] = true
				}
			}
		}
		for _, id := range users {
			if !active[id] {
				return evidence, ErrInput
			}
		}
	}
	return evidence, nil
}

// bindEvent reserves a unique location/event binding and applies retained callbacks atomically.
func (w *Workflow) bindEvent(ctx context.Context, tx state.Tx, j *LicensingJob, eventID string) error {
	if eventID == "" {
		return ErrInput
	}
	var binding string
	err := readState(ctx, tx, eventKey(j.LocationID, eventID), &binding)
	if err == nil && binding != j.ID {
		return ErrInput
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	j.EventID = eventID
	j.State = JobAccepted
	j.NextPollAt = tx.Now().Add(5 * time.Minute)
	if err := putState(ctx, tx, eventKey(j.LocationID, eventID), j.ID); err != nil {
		return err
	}
	return w.applyRetainedNotifications(ctx, tx, j)
}

// ReconcileJob explicitly fences a stopped worker after an expired lease (or an
// uncertain response). Supply a known eventID to bind observed Apple evidence;
// otherwise retain uncertain state. abandon requires an operator note and makes
// no claim that remote work was undone. It never sends or re-enables a POST.
// Stop the original process first: local fencing cannot cancel a remote request.
func (w *Workflow) ReconcileJob(ctx context.Context, location, id, eventID, note string, abandon bool) (LicensingJob, error) {
	if note == "" || (abandon && eventID != "") {
		return LicensingJob{}, ErrInput
	}
	var out LicensingJob
	err := w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		if _, err := w.readLocation(ctx, tx, location); err != nil {
			return err
		}
		if err := readState(ctx, tx, jobKey(location, id), &out); err != nil {
			return err
		}
		if out.State != JobSubmitting && out.State != JobUncertain && out.State != JobQueued && out.State != JobAccepted {
			return ErrInput
		}
		var active locationWorker
		err := readState(ctx, tx, locationKey(location, "worker"), &active)
		if err == nil {
			if active.JobID != id || tx.Now().Before(active.Until) {
				return ErrJobBusy
			}
			if err := tx.Delete(ctx, locationKey(location, "worker")); err != nil {
				return err
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if out.Fence == math.MaxUint64 {
			return ErrJobFence
		}
		out.Fence++
		out.Worker = ""
		out.LeaseUntil = time.Time{}
		out.ReconciliationNote = note
		out.UpdatedAt = tx.Now()
		if abandon {
			out.State = JobAbandoned
		} else if eventID != "" {
			if out.EventID != "" && out.EventID != eventID {
				return ErrInput
			}
			if err := w.bindEvent(ctx, tx, &out, eventID); err != nil {
				return err
			}
		} else if out.State != JobAccepted {
			out.State = JobUncertain
		}
		if out.State == JobUncertain {
			if err := putState(ctx, tx, locationKey(location, "worker"), locationWorker{JobID: id, Fence: out.Fence, Until: tx.Now()}); err != nil {
				return err
			}
		}
		return putState(ctx, tx, jobKey(location, id), out)
	})
	return out, err
}

// RecoverLicensingToken explicitly renews an expired token while an uncertain or
// crashed submission blocks ordinary writers. Stop the original worker first.
// A read-only probe validates the SAME UID and MDM ownership. One atomic commit
// fences the old worker and replaces credentials while preserving job intent,
// event binding and evidence. The job remains uncertain; no POST is replayed.
func (w *Workflow) RecoverLicensingToken(ctx context.Context, location string, revision uint64, jobID, token string) (Location, error) {
	if revision == 0 || jobID == "" {
		return Location{}, ErrInput
	}
	return w.register(ctx, location, revision, token, false, jobID)
}

// recoverLicensingWorker fences an expired stopped worker without clearing remote uncertainty.
func (w *Workflow) recoverLicensingWorker(ctx context.Context, tx state.Tx, location, id string) error {
	var active locationWorker
	if err := readState(ctx, tx, locationKey(location, "worker"), &active); err != nil {
		return err
	}
	if active.JobID != id || tx.Now().Before(active.Until) {
		return ErrJobBusy
	}
	var j LicensingJob
	if err := readState(ctx, tx, jobKey(location, id), &j); err != nil {
		return err
	}
	if j.State != JobSubmitting && j.State != JobUncertain && j.State != JobAccepted {
		return ErrInput
	}
	if j.Fence == math.MaxUint64 {
		return ErrJobFence
	}
	j.Fence++
	j.Worker = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = tx.Now()
	if j.State != JobAccepted {
		j.State = JobUncertain
	}
	if err := putState(ctx, tx, jobKey(location, id), j); err != nil {
		return err
	}
	return putState(ctx, tx, locationKey(location, "worker"), locationWorker{JobID: id, Fence: j.Fence, Until: tx.Now()})
}

// PollJob reads aggregate status at the stored due time. PENDING waits at least
// 30 seconds. Even COMPLETE requires matching positive counts, the expected event
// type and no observed callback failures. Late callbacks never resurrect success.
func (w *Workflow) PollJob(ctx context.Context, location, id, worker string, ttl time.Duration) (LicensingJob, error) {
	j, err := w.reserveJob(ctx, location, id, worker, ttl, false)
	if err != nil {
		return j, err
	}
	l, err := w.Load(ctx, location)
	var status Status
	if err == nil {
		var c *Client
		_, c, err = w.session(ctx, location, l.Revision, false)
		if err == nil {
			status, err = c.EventStatus(ctx, j.EventID)
		}
	}
	out, persistErr := w.finishJob(ctx, j, func(tx state.Tx, out *LicensingJob) error {
		out.NextPollAt = tx.Now().Add(30 * time.Second)
		// A failed GET still commits the minimum retry interval; PollJob returns err.
		if err == nil {
			if status.EventType != string(out.Request.Operation) {
				return ErrProtocol
			}
			out.Status = &status
			out.StatusRaw = status.Raw
			out.NextPollAt = tx.Now().Add(30 * time.Second)
			switch status.EventStatus {
			case "COMPLETE":
				if status.Successful() && !out.NotificationFailure && (out.Request.Operation == JobRevoke || status.NumRequested == out.expectedCount()) {
					out.State = JobSucceeded
				} else {
					out.State = JobFailed
				}
			case "FAILED":
				out.State = JobFailed
			case "PENDING":
			default:
				return ErrProtocol
			}
		}
		return nil
	})
	if persistErr != nil {
		return j, persistErr
	}
	return out, err
}

// expectedCount counts requested discrete tasks; revoke-all has no locally known total.
func (j LicensingJob) expectedCount() int64 {
	if j.Request.Assets != nil {
		return int64(len(j.Request.Assets.Assets)) * int64(len(j.Request.Assets.ClientUserIDs)+len(j.Request.Assets.SerialNumbers))
	}
	if j.Request.Users != nil {
		return int64(len(j.Request.Users.Users))
	}
	return 0
}
