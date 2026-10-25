package axmassignment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// claim serializes each workflow across replicas and fences obsolete account configurations.
func (c *Controller) claim(ctx context.Context, id string, now time.Time) (Workflow, string, error) {
	var w Workflow
	owner := inventory.ID()
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		var e error
		w, e = read[Workflow](ctx, tx, "assignment/"+id)
		if e != nil {
			return e
		}
		if w.State == "complete" || w.State == "fenced" || w.NextAttempt.After(now) {
			return inventory.ErrStopped
		}
		l, e := read[lease](ctx, tx, "assignment-lease/"+id)
		if e != nil && !errors.Is(e, inventory.ErrNotFound) {
			return e
		}
		if l.Until.After(now) {
			return inventory.ErrLease
		}
		a, key, e := snapshot(ctx, tx, w.Binding.AccountID)
		b, be := read[Binding](ctx, tx, "assignment-binding/"+w.Binding.AccountID)
		if e != nil && !errors.Is(e, inventory.ErrNotFound) && !errors.Is(e, inventory.ErrStopped) {
			return e
		}
		if be != nil && !errors.Is(be, inventory.ErrNotFound) {
			return be
		}
		if e != nil || be != nil || b != w.Binding || a.Revision != w.Binding.Revision || fingerprint(a, key) != w.Binding.Fingerprint {
			w.State = "fenced"
			w.Error = "account_or_service_replaced"
			w.UpdatedAt = now
			return write(ctx, tx, "assignment/"+id, w)
		}
		if w.State == "submitting" {
			w.State = "uncertain"
			w.Error = "submission_interrupted"
		}
		if e = write(ctx, tx, "assignment/"+id, w); e != nil {
			return e
		}
		return write(ctx, tx, "assignment-lease/"+id, lease{owner, now.Add(2 * time.Minute)})
	})
	return w, owner, err
}

// commit checks lease, account incarnation, key and binding before publishing any evidence.
func (c *Controller) commit(ctx context.Context, w *Workflow, owner string, evidence *Evidence, release bool) error {
	err := c.Repository.Backend.Update(ctx, func(tx inventory.Tx) error {
		now := time.Now().UTC()
		l, e := read[lease](ctx, tx, "assignment-lease/"+w.ID)
		if e != nil {
			return e
		}
		if l.Owner != owner || !l.Until.After(now) {
			return inventory.ErrLease
		}
		a, key, e := snapshot(ctx, tx, w.Binding.AccountID)
		if e != nil {
			return e
		}
		b, e := read[Binding](ctx, tx, "assignment-binding/"+w.Binding.AccountID)
		if e != nil {
			return e
		}
		if b != w.Binding || fingerprint(a, key) != w.Binding.Fingerprint {
			return inventory.ErrStopped
		}
		for _, observation := range w.observations {
			if _, e = c.Repository.ObserveTx(ctx, tx, "", observation); e != nil {
				return e
			}
		}
		if evidence != nil {
			const chunkSize = 4 << 20
			count := (len(evidence.CSV) + chunkSize - 1) / chunkSize
			for i := 0; i < count; i++ {
				if e = write(ctx, tx, "assignment-log/"+w.ID+"/"+strconv.Itoa(i), evidence.CSV[i*chunkSize:min((i+1)*chunkSize, len(evidence.CSV))]); e != nil {
					return e
				}
			}
			if e = write(ctx, tx, "assignment-evidence/"+w.ID, count); e != nil {
				return e
			}
		}
		w.UpdatedAt = now
		if e = write(ctx, tx, "assignment/"+w.ID, w); e != nil {
			return e
		}
		if release {
			return tx.Delete(ctx, "assignment-lease/"+w.ID)
		}
		return write(ctx, tx, "assignment-lease/"+w.ID, lease{owner, now.Add(2 * time.Minute)})
	})
	if err == nil {
		w.observations = nil
	}
	return err
}

// Step performs one bounded observation or submission attempt. No network request runs in a transaction.
// Errors are stable codes; remote bodies, credentials and signed download URLs never enter summaries.
func (c *Controller) Step(ctx context.Context, id string) error {
	w, owner, err := c.claim(ctx, id, time.Now().UTC())
	if err != nil {
		return err
	}
	if w.State == "fenced" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// Revision is checked again at every commit, including immediately before POST.
	a, err := c.Repository.Account(ctx, w.Binding.AccountID)
	if err != nil {
		return err
	}
	key, err := c.Repository.PrivateKey(ctx, a.ID)
	if err != nil {
		return err
	}
	client, err := c.Client(ctx, a, key)
	if err != nil {
		return c.fail(ctx, &w, owner, "client_unavailable")
	}
	if w.State == "queued" {
		if _, err = client.GetMDMServer(ctx, w.Binding.ServiceID, axm.GetOptions{}); err != nil {
			return c.fail(ctx, &w, owner, "service_lookup_failed")
		}
		serials := make([]string, len(w.Targets))
		for i, t := range w.Targets {
			serials[i] = t.Serial
		}
		devices, e := client.ResolveOrgDevicesBySerial(ctx, serials)
		if e != nil {
			return c.fail(ctx, &w, owner, "device_lookup_failed")
		}
		ids := make([]string, len(devices))
		for i, d := range devices {
			w.Targets[i].DeviceID = d.ID
			ids[i] = d.ID
			if err = c.observe(&w, d, a.DeviceTTL); err != nil {
				return err
			}
		}
		typ, service := axm.ActivityAssignDevices, w.Binding.ServiceID
		if w.Operation == "unassign" {
			typ, service = axm.ActivityUnassignDevices, ""
			for _, id := range ids {
				link, e := client.GetAssignedServerLinkage(ctx, id)
				if e != nil {
					return c.fail(ctx, &w, owner, "assignment_read_failed")
				}
				if link.ID != w.Binding.ServiceID {
					return c.fail(ctx, &w, owner, "outside_configured_service")
				}
			}
		}
		req, e := axm.NewActivityRequest(typ, service, ids, time.Time{}, time.Now().UTC())
		if e != nil {
			return e
		}
		started := time.Now().UTC()
		w.SubmissionStartedAt = &started
		w.State = "submitting"
		w.Error = ""
		if err = c.commit(ctx, &w, owner, nil, false); err != nil {
			return err
		}
		act, e := client.CreateOrgDeviceActivity(ctx, req)
		if e != nil || act == nil || act.ID == "" {
			w.State = "uncertain"
			return c.fail(ctx, &w, owner, "submission_uncertain")
		}
		now := time.Now().UTC()
		w.ActivityID = act.ID
		w.AcceptedAt = &now
		w.State = "accepted"
		// Persist the accepted ID before another Apple request. A failed commit leaves submitting.
		if err = c.commit(ctx, &w, owner, nil, false); err != nil {
			return err
		}
	}
	var evidence *Evidence
	if w.ActivityID != "" {
		act, e := client.GetOrgDeviceActivity(ctx, w.ActivityID, axm.GetOptions{})
		if e != nil {
			return c.fail(ctx, &w, owner, "activity_read_failed")
		}
		if act.Terminal() {
			w.TerminalStatus = act.Attributes.Status
			w.TerminalSubStatus = act.Attributes.SubStatus
			w.State = "terminal"
			// Commit terminal evidence before attempting a log download.
			if err = c.commit(ctx, &w, owner, nil, false); err != nil {
				return err
			}
			if !w.LogComplete && act.Attributes.DownloadURL != "" {
				body, e := client.FetchActivityLog(ctx, act.Attributes.DownloadURL)
				if e != nil {
					return c.fail(ctx, &w, owner, "log_download_failed")
				}
				raw, e := io.ReadAll(body)
				closeErr := body.Close()
				if e != nil || closeErr != nil {
					return c.fail(ctx, &w, owner, "log_read_failed")
				}
				serials := make([]string, len(w.Targets))
				for i, t := range w.Targets {
					serials[i] = t.Serial
				}
				log, verifyErr := axm.VerifyActivityLog(bytes.NewReader(raw), w.Binding.Columns, serials)
				evidence = &Evidence{CSV: raw, Log: log}
				w.LogComplete = log.Complete
				w.LogSucceeded = log.Succeeded
				for i := range w.Targets {
					for _, row := range log.Rows {
						if row[w.Binding.Columns.Serial] == w.Targets[i].Serial {
							w.Targets[i].Outcome = row[w.Binding.Columns.Outcome]
						}
					}
				}
				if verifyErr != nil {
					w.Error = "log_incomplete"
				} else {
					w.Error = ""
				}
				if err = c.commit(ctx, &w, owner, evidence, false); err != nil {
					return err
				}
			}
		}
	}
	// Observe each addressed resource; a missing resource or 404 is not evidence of convergence.
	for i := range w.Targets {
		device, e := client.GetOrgDevice(ctx, w.Targets[i].DeviceID, axm.GetOptions{})
		if e != nil {
			return c.fail(ctx, &w, owner, "device_read_failed")
		}
		if device.ID != w.Targets[i].DeviceID || device.Attributes.SerialNumber != w.Targets[i].Serial {
			return c.fail(ctx, &w, owner, "device_identity_changed")
		}
		if err = c.observe(&w, *device, a.DeviceTTL); err != nil {
			return err
		}
		link, e := client.GetAssignedServerLinkage(ctx, device.ID)
		if e != nil {
			return c.fail(ctx, &w, owner, "assignment_read_failed")
		}
		now := time.Now().UTC()
		w.Targets[i].ObservedAt = &now
		w.Targets[i].ObservedService = link.ID
		desired := w.Binding.ServiceID
		if w.Operation == "unassign" {
			desired = ""
		}
		w.Targets[i].Converged = link.ID == desired
	}
	if w.TerminalStatus != "" && w.LogComplete {
		settled := true
		for _, t := range w.Targets {
			if t.Outcome == w.Binding.Columns.Success && !t.Converged {
				settled = false
			}
		}
		if settled {
			w.State = "complete"
		}
	}
	w.NextAttempt = time.Now().UTC().Add(5 * time.Second)
	return c.commit(ctx, &w, owner, nil, true)
}

// fail preserves successful evidence and schedules a read-only retry for uncertain submissions.
func (c *Controller) fail(ctx context.Context, w *Workflow, owner, code string) error {
	w.Error = code
	w.NextAttempt = time.Now().UTC().Add(5 * time.Second)
	return c.commit(ctx, w, owner, nil, true)
}

// Run resumes durable work until cancellation; replicas coordinate through backend leases.
func (c *Controller) Run(ctx context.Context) error {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		after := ""
		for {
			rows, err := c.Repository.Backend.Scan(ctx, "assignment/", after, 128)
			if err != nil {
				return err
			}
			for _, row := range rows {
				var w Workflow
				if err = json.Unmarshal(row.Value, &w); err != nil {
					return err
				}
				if w.State != "complete" && w.State != "fenced" && !w.NextAttempt.After(time.Now().UTC()) {
					err = c.Step(ctx, w.ID)
					if err != nil && !errors.Is(err, inventory.ErrLease) && !errors.Is(err, inventory.ErrStopped) && !errors.Is(err, inventory.ErrNotFound) {
						return err
					}
				}
				after = row.Key
			}
			if len(rows) < 128 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// observe retains complete source JSON under the existing account-scoped resource provenance.
func (c *Controller) observe(w *Workflow, device axm.OrgDevice, ttl time.Duration) error {
	o, e := inventory.ResourceObservation(source.Reference{Kind: "axm.device", AccountID: w.Binding.AccountID, ResourceID: device.ID}, device.Raw, time.Now().UTC(), ttl)
	if e == nil {
		w.observations = append(w.observations, o)
	}
	return e
}
