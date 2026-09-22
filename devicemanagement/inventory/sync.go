package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// ClientFactory lets applications configure HTTP trust without persisting transports.
type ClientFactory func(context.Context, Account, []byte) (*axm.Client, error)

// Syncer executes a durable serial queue. Each outbound operation is bounded below the lease duration.
type Syncer struct {
	Repository *Repository
	Client     ClientFactory
	// JobRetention defaults to 30 days.
	JobRetention time.Duration
}

// NewClient creates an independently authenticated connection; tokens never cross accounts.
func NewClient(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
	return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, Scope: a.Scope, BaseURL: a.BaseURL, TokenURL: a.TokenURL, PageCap: 100000})
}

// RunOne claims and processes one job. ErrNotFound means the queue is empty.
func (s *Syncer) RunOne(ctx context.Context) (Job, error) {
	r := s.Repository
	j, err := r.claim(ctx, ID(), time.Now().UTC())
	if err != nil {
		return j, err
	}
	if j.State != "running" {
		return j, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = r.release(cleanup, j)
	}()
	a, err := r.Account(ctx, j.AccountID)
	if err != nil {
		return j, err
	}
	key, err := r.PrivateKey(ctx, a.ID)
	if err != nil {
		return j, err
	}
	factory := s.Client
	if factory == nil {
		factory = NewClient
	}
	c, err := factory(ctx, a, key)
	if err == nil {
		err = s.sync(ctx, c, a, &j)
	}
	if errors.Is(err, ErrStopped) || errors.Is(err, ErrLease) || ctx.Err() != nil {
		return j, err
	}
	now := time.Now().UTC()
	j.FinishedAt = &now
	j.State = "success"
	if err != nil {
		j.Error = errorCode(err)
		j.Failed++
		j.State = "failed"
	}
	if j.Failed > 0 {
		if j.Devices > 0 || j.Coverage > 0 {
			j.State = "partial"
		} else {
			j.State = "failed"
		}
	}
	saveErr := r.commitJob(ctx, &j, nil)
	if saveErr != nil {
		return j, saveErr
	}
	return j, err
}

// Run polls the serial queue and coalesces scheduled work until shutdown.
func (s *Syncer) Run(ctx context.Context) error {
	retention := s.JobRetention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	nextPrune := time.Time{}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		now := time.Now().UTC()
		if !now.Before(nextPrune) {
			if _, err := s.Repository.PruneJobs(ctx, now, retention); err == nil {
				nextPrune = now.Add(time.Hour)
			}
		}
		_ = s.Repository.Tick(ctx, now)
		_, err := s.RunOne(ctx)
		if err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// bounded keeps one outbound operation shorter than the worker lease.
func bounded[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return fn(ctx)
}

// sync commits enumeration pages before enrichment and conditional built-in MDM discovery.
func (s *Syncer) sync(ctx context.Context, c *axm.Client, a Account, j *Job) error {
	r := s.Repository
	if j.Stage == "devices" {
		var prior axm.Page[axm.OrgDevice]
		if len(j.Checkpoint) > 0 {
			if err := json.Unmarshal(j.Checkpoint, &prior); err != nil {
				return err
			}
		}
		for {
			if err := r.commitJob(ctx, j, nil); err != nil {
				return err
			}
			page, err := bounded(ctx, func(ctx context.Context) (axm.Page[axm.OrgDevice], error) {
				if len(j.Checkpoint) > 0 {
					return axm.NextPage(ctx, c, prior)
				}
				return c.ListOrgDevices(ctx, axm.ListOptions{Limit: 1000})
			})
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			pageKey := "jobpage/" + j.ID + "/" + digest("devices|"+page.Links.Self)
			j.Devices += len(page.Items)
			if page.HasNext() {
				checkpoint := page
				checkpoint.Items = nil
				checkpoint.Included = nil
				j.Checkpoint, err = json.Marshal(checkpoint)
				if err != nil {
					return err
				}
			} else {
				j.Checkpoint = nil
				j.Stage = "enrich"
				j.Cursor = ""
			}
			err = r.commitJob(ctx, j, func(tx Tx) error {
				if _, e := tx.Get(ctx, pageKey); e == nil {
					return axm.ErrNextLink
				} else if !errors.Is(e, ErrNotFound) {
					return e
				}
				if e := put(ctx, tx, pageKey, true); e != nil {
					return e
				}
				for _, d := range page.Items {
					raw := d.Raw
					if len(raw) == 0 {
						raw, _ = json.Marshal(d)
					}
					o, e := ResourceObservation(SourceReference{"axm.device", a.ID, d.ID}, raw, now, a.DeviceTTL)
					if e != nil {
						return e
					}
					o.Generation = j.ID
					if _, e = r.ObserveTx(ctx, tx, d.Attributes.SerialNumber, o); e != nil {
						return e
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			if !page.HasNext() {
				break
			}
			if page.Links.Next == prior.Links.Next && page.Meta.Paging.NextCursor == prior.Meta.Paging.NextCursor {
				return axm.ErrNextLink
			}
			prior = page
		}
	}
	if j.Stage == "enrich" {
		for {
			page, err := r.Devices(ctx, DeviceQuery{AccountID: a.ID, Cursor: j.Cursor, Limit: 50})
			if err != nil {
				return err
			}
			for _, d := range page.Items {
				for _, o := range d.Sources {
					if o.Source.Kind != "axm.device" || o.Source.AccountID != a.ID || o.Disconnected {
						continue
					}
					if err := s.enrich(ctx, c, a, j, d, o); err != nil {
						return err
					}
				}
				j.Cursor = d.ID
				if err := r.commitJob(ctx, j, nil); err != nil {
					return err
				}
			}
			if page.NextCursor == "" {
				break
			}
		}
		j.Stage = "apple_mdm"
		j.Cursor = ""
	}
	if j.Stage == "apple_mdm" {
		if err := s.appleMDM(ctx, c, a, j); err != nil {
			return err
		}
		j.Stage = "complete"
	}

	return nil
}

// enrich refreshes device detail, assignment and all coverage plans independently.
func (s *Syncer) enrich(ctx context.Context, c *axm.Client, a Account, j *Job, d DeviceRecord, listed Observation) error {
	r := s.Repository
	id := listed.Source.ResourceID
	now := time.Now().UTC()
	save := func(kind string, raw json.RawMessage, ttl time.Duration) error {
		o, e := ResourceObservation(SourceReference{kind, a.ID, id}, raw, time.Now().UTC(), ttl)
		if e != nil {
			return e
		}
		o.Generation = j.ID
		return r.commitJob(ctx, j, func(tx Tx) error { _, e := r.ObserveTx(ctx, tx, d.SerialNumber, o); return e })
	}
	failed := func(kind string, err error) error {
		if axm.IsUnauthorized(err) {
			return err
		}
		j.Failed++
		// Error text deliberately excludes response bodies, serials, and endpoint hosts.
		return r.commitJob(ctx, j, func(tx Tx) error {
			current, e := get[DeviceRecord](ctx, tx, "device/"+d.ID)
			if e != nil {
				return e
			}
			ref := SourceReference{kind, a.ID, id}
			o := current.Sources[ref.Key()]
			o.Source = ref
			o.AttemptedAt = time.Now().UTC()
			o.Error = errorCode(err)
			current.Sources[ref.Key()] = o
			if e := put(ctx, tx, "source/"+ref.Key(), current.ID); e != nil {
				return e
			}
			return r.saveRecord(ctx, tx, current)
		})
	}
	fresh := func(kind string, ttl time.Duration) bool {
		o, ok := d.Sources[(SourceReference{kind, a.ID, id}).Key()]
		return ok && !o.Absent && !o.Disconnected && o.Error == "" && now.Before(o.ObservedAt.Add(ttl)) && !j.Force
	}
	if !fresh("axm.detail", a.DeviceTTL) || listed.Generation != j.ID {
		if err := r.commitJob(ctx, j, nil); err != nil {
			return err
		}
		detail, err := bounded(ctx, func(ctx context.Context) (*axm.OrgDevice, error) { return c.GetOrgDevice(ctx, id, axm.GetOptions{}) })
		if err != nil {
			if axm.IsNotFound(err) && listed.Generation != j.ID {
				// Only after a complete enumeration plus a confirming detail lookup.
				return r.commitJob(ctx, j, func(tx Tx) error {
					current, e := get[DeviceRecord](ctx, tx, "device/"+d.ID)
					if e != nil {
						return e
					}
					for k, o := range current.Sources {
						if o.Source.AccountID == a.ID && o.Source.ResourceID == id {
							o.Absent = true
							current.Sources[k] = o
						}
					}
					rebuild(&current)
					return r.saveRecord(ctx, tx, current)
				})
			}
			if e := failed("axm.detail", err); e != nil {
				return e
			}
		} else {
			if err := save("axm.detail", detail.Raw, a.DeviceTTL); err != nil {
				return err
			}
		}
	}
	if !fresh("axm.assignment", a.DeviceTTL) {
		if err := r.commitJob(ctx, j, nil); err != nil {
			return err
		}
		server, err := bounded(ctx, func(ctx context.Context) (*axm.MDMServer, error) {
			server, err := c.GetAssignedServer(ctx, id, axm.GetOptions{})
			if axm.IsNotFound(err) && listed.Generation == j.ID {
				return nil, nil
			}
			return server, err
		})
		if err != nil {
			if e := failed("axm.assignment", err); e != nil {
				return e
			}
		} else {
			assignment := map[string]any{"mdm_assigned": server != nil && server.ID != ""}
			if server != nil && server.ID != "" {
				assignment["mdm_server_id"] = server.ID
				assignment["mdm_server"] = server
				assignment["mdm_server_raw"] = server.Raw
			}
			raw, e := json.Marshal(assignment)
			if e != nil {
				return e
			}
			if e := save("axm.assignment", raw, a.DeviceTTL); e != nil {
				return e
			}
		}
	}
	old, seen := d.Sources[(SourceReference{"axm.coverage", a.ID, id}).Key()]
	if !fresh("axm.coverage", a.CoverageTTL) && !(a.NeverRefetchCoverage && seen && !old.ObservedAt.IsZero() && !j.Force) {
		if a.CoverageBudget > 0 && j.CoverageAttempts >= a.CoverageBudget {
			j.Failed++
			j.Error = "coverage_budget_exhausted"
			return nil
		}
		if err := r.commitJob(ctx, j, nil); err != nil {
			return err
		}
		j.CoverageAttempts++
		if err := r.commitJob(ctx, j, nil); err != nil {
			return err
		}
		plans, err := bounded(ctx, func(ctx context.Context) ([]json.RawMessage, error) {
			p, e := c.ListAppleCareCoverage(ctx, id, axm.ListOptions{Limit: 1000})
			if e != nil {
				return nil, e
			}
			out := []json.RawMessage{}
			for page, e := range axm.Pages(ctx, c, p) {
				if e != nil {
					return nil, e
				}
				for _, plan := range page.Items {
					raw := plan.Raw
					if len(raw) == 0 {
						raw, _ = json.Marshal(plan)
					}
					out = append(out, raw)
				}
			}
			return out, nil
		})
		if err != nil {
			if e := failed("axm.coverage", err); e != nil {
				return e
			}
		} else {
			raw, e := json.Marshal(plans)
			if e != nil {
				return e
			}
			j.Coverage++
			if e := save("axm.coverage", raw, a.CoverageTTL); e != nil {
				return e
			}
		}
	}
	return nil
}

// errorCode classifies failures without persisting response bodies or connection identifiers.
func errorCode(err error) string {
	var api *axm.Error
	if errors.As(err, &api) {
		return fmt.Sprintf("apple_http_%d", api.Status)
	}
	if axm.IsUnauthorized(err) {
		return "apple_authentication"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "sync_error"
}

// appleMDM discovers Apple's built-in MDM only when the account actually has that service.
func (s *Syncer) appleMDM(ctx context.Context, c *axm.Client, a Account, j *Job) error {
	if strings.HasPrefix(a.ClientID, "SCHOOLAPI.") || a.Scope == "school.api" {
		return nil
	}
	r := s.Repository
	if err := r.commitJob(ctx, j, nil); err != nil {
		return err
	}
	enabled, err := bounded(ctx, func(ctx context.Context) (bool, error) {
		p, e := c.ListMDMServers(ctx, axm.ListOptions{Limit: 1000})
		if e != nil {
			return false, e
		}
		found := false
		for page, e := range axm.Pages(ctx, c, p) {
			if e != nil {
				return false, e
			}
			for _, server := range page.Items {
				found = found || server.Attributes.ServerType == axm.MDMServerTypeAppleMDM
			}
		}
		return found, nil
	})
	if err != nil {
		j.Failed++
		j.Error = errorCode(err)
		return r.commitJob(ctx, j, nil)
	}
	if !enabled {
		return nil
	}
	var prior axm.Page[axm.MDMDevice]
	if len(j.Checkpoint) > 0 {
		if err := json.Unmarshal(j.Checkpoint, &prior); err != nil {
			return err
		}
	}
	for {
		if err := r.commitJob(ctx, j, nil); err != nil {
			return err
		}
		page, err := bounded(ctx, func(ctx context.Context) (axm.Page[axm.MDMDevice], error) {
			if len(j.Checkpoint) > 0 {
				return axm.NextPage(ctx, c, prior)
			}
			return c.ListMDMDevices(ctx, axm.ListOptions{Limit: 1000})
		})
		if err != nil {
			j.Failed++
			j.Error = errorCode(err)
			return r.commitJob(ctx, j, nil)
		}
		pageKey := "jobpage/" + j.ID + "/" + digest("apple_mdm|"+page.Links.Self)
		if _, err := r.Backend.Get(ctx, pageKey); err == nil {
			return axm.ErrNextLink
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		commitPage := func(tx Tx) error { return put(ctx, tx, pageKey, true) }
		for _, device := range page.Items {
			if err := r.commitJob(ctx, j, func(tx Tx) error {
				o, e := ResourceObservation(SourceReference{"axm.mdm", a.ID, device.ID}, device.Raw, time.Now().UTC(), a.DeviceTTL)
				if e != nil {
					return e
				}
				_, e = r.ObserveTx(ctx, tx, device.Attributes.SerialNumber, o)
				return e
			}); err != nil {
				return err
			}
			detail, err := bounded(ctx, func(ctx context.Context) (*axm.MDMDeviceDetail, error) {
				return c.GetMDMDeviceDetails(ctx, device.ID, axm.GetOptions{})
			})
			if err != nil {
				j.Failed++
				continue
			}
			if err := r.commitJob(ctx, j, func(tx Tx) error {
				o, e := ResourceObservation(SourceReference{"axm.mdm.detail", a.ID, device.ID}, detail.Raw, time.Now().UTC(), a.DeviceTTL)
				if e != nil {
					return e
				}
				_, e = r.ObserveTx(ctx, tx, device.Attributes.SerialNumber, o)
				return e
			}); err != nil {
				return err
			}
		}
		if !page.HasNext() {
			j.Checkpoint = nil
			return r.commitJob(ctx, j, commitPage)
		}
		checkpoint := page
		checkpoint.Items = nil
		checkpoint.Included = nil
		j.Checkpoint, err = json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if err := r.commitJob(ctx, j, commitPage); err != nil {
			return err
		}
		prior = page
	}
}
