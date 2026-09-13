package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

func (a *App) issuedIdentity(ctx context.Context, hash string) (identityEvidence, error) {
	var evidence identityEvidence
	rec, err := a.protocol.Get(ctx, "issued-identity:"+hash)
	if err == nil {
		if err = json.Unmarshal(rec.Value, &evidence); err != nil {
			return evidence, wrapError(err)
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		return evidence, wrapError(err)
	}
	if evidence.Issuer != "" {
		return evidence, nil
	}
	// Adopted deployments may have evidence written before issuer IDs were
	// recorded. The existing certificate registry provides an exact hash index.
	index, err := a.protocol.Get(ctx, "pki/fingerprint/"+hash)
	if err != nil {
		return evidence, wrapError(err)
	}
	rec, err = a.protocol.Get(ctx, string(index.Value))
	if err != nil {
		return evidence, wrapError(err)
	}
	var certificate revocation.Certificate
	if err = json.Unmarshal(rec.Value, &certificate); err != nil {
		return evidence, wrapError(err)
	}
	evidence.Issuer = certificate.Issuer
	evidence.NotAfter = certificate.NotAfter
	if evidence.Method == "" {
		evidence.Method = certificate.Provenance.Source
	}
	return evidence, nil
}

func (a *App) enabledDevices(ctx context.Context) ([]storage.Enrollment, error) {
	out := []storage.Enrollment{}
	enabled := true
	p := paging.Page{Limit: 100}
	for {
		page, err := a.Store.List(
			ctx,
			storage.EnrollmentQuery{Channel: mdm.ChannelDevice, Enabled: &enabled},
			p,
		)
		if err != nil {
			return nil, wrapError(err)
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out, nil
		}
		p.Cursor = page.NextCursor
	}
}

func (a *App) startIssuerRollover(ctx context.Context, id, rev string) (lifecycle.Rollover, error) {
	if id != a.cfg.Setup.IssuerID || a.enroll == nil {
		return lifecycle.Rollover{}, lifecycle.ErrConflict
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return lifecycle.Rollover{}, wrapError(err)
	}
	var cohort []string
	for _, e := range devices {
		cohort = append(cohort, e.ID.ID)
	}
	job, err := a.Certificates.PrepareRollover(ctx, id, rev, cohort)
	if err != nil {
		return job, wrapError(err)
	}
	if _, err = a.managedIssuer(ctx, rev); err != nil {
		return job, wrapError(err)
	}
	if _, err = a.managedRoots(ctx); err != nil {
		return job, wrapError(err)
	}
	result, err := a.Certificates.ActivateRollover(ctx, id, rev)
	return result, wrapError(err)
}

func (a *App) renewDeviceIdentities(ctx context.Context) error {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		if err := a.identityRenewalPass(ctx); err != nil && ctx.Err() == nil {
			a.cfg.Logger.WarnContext(ctx, "device identity renewal scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (a *App) identityRenewalPass(ctx context.Context) error {
	if a.enroll == nil {
		return nil
	}
	item, err := a.Certificates.Get(ctx, a.cfg.Setup.IssuerID)
	if err != nil {
		return wrapError(err)
	}
	rolling := false
	for _, rev := range item.Revisions {
		job, err := a.Certificates.Rollover(ctx, item.ID, rev.ID)
		if errors.Is(err, lifecycle.ErrNotFound) {
			continue
		}
		if err != nil {
			return wrapError(err)
		}
		if job.Phase == "prepared" {
			job, err = a.startIssuerRollover(ctx, item.ID, rev.ID)
			if err != nil {
				return wrapError(err)
			}
		}
		if job.Phase == "migrating" {
			rolling = true
			if err = a.advanceRollover(ctx, job); err != nil {
				return wrapError(err)
			}
		}
	}
	if err := a.reconcileDeviceRenewals(ctx); err != nil {
		return wrapError(err)
	}
	if rolling {
		return nil
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return wrapError(err)
	}
	for _, e := range devices {
		evidence, err := a.issuedIdentity(ctx, e.CertHash)
		if err != nil {
			continue
		}
		if a.cfg.Clock.Now().Before(evidence.NotAfter.Add(-60 * 24 * time.Hour)) {
			continue
		}
		if err = a.renewOneIdentity(ctx, e); err != nil && !errors.Is(err, lifecycle.ErrConflict) {
			a.cfg.Logger.WarnContext(
				ctx,
				"device identity renewal pending",
				"device",
				e.ID.ID,
				"error",
				err,
			)
		}
	}
	return nil
}

func (a *App) advanceRollover(ctx context.Context, job lifecycle.Rollover) error {
	target, err := a.managedIssuer(ctx, job.To)
	if err != nil {
		return wrapError(err)
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return wrapError(err)
	}
	known := map[string]bool{}
	cursor := ""
	remaining := false
	for {
		page, next, err := a.Certificates.Migrations(ctx, job.IssuerID, job.To, cursor, 100)
		if err != nil {
			return wrapError(err)
		}
		for _, m := range page {
			known[m.Device] = true
			if m.Phase == "confirmed" || m.Phase == "disabled" {
				continue
			}
			remaining = true
			if m.Phase == "blocked" || a.cfg.Clock.Now().Before(m.NextAttempt) {
				continue
			}
			// A persisted compare-and-swap claim prevents two workers from issuing
			// profiles concurrently. A crash releases the claim after five minutes.
			m.NextAttempt = a.cfg.Clock.Now().Add(5 * time.Minute)
			if err = a.Certificates.SaveMigration(
				ctx,
				job.IssuerID,
				job.To,
				m,
			); errors.Is(
				err,
				lifecycle.ErrConflict,
			) {
				continue
			} else if err != nil {
				return wrapError(err)
			}
			m.Generation++
			e, err := a.Store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: m.Device})
			switch {
			case errors.Is(err, storage.ErrNotFound) || (err == nil && !e.Enabled):
				m.Phase = "disabled"
			case err != nil:
				return wrapError(err)
			default:
				if err = a.progressMigration(ctx, target, e, &m, true); err != nil {
					m.Reason = "migration will retry"
					m.NextAttempt = a.cfg.Clock.Now().Add(time.Hour)
				}
			}
			if err = a.Certificates.SaveMigration(
				ctx,
				job.IssuerID,
				job.To,
				m,
			); err != nil &&
				!errors.Is(err, lifecycle.ErrConflict) {
				return wrapError(err)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	// Catch enrollments created between cohort discovery and default activation.
	// Unknown issuer evidence blocks completion instead of being assumed migrated.
	for _, e := range devices {
		evidence, err := a.issuedIdentity(ctx, e.CertHash)
		if err == nil && evidence.Issuer == cms.Fingerprint(target.enrollment.caCert) {
			continue
		}
		remaining = true
		if !known[e.ID.ID] {
			m := lifecycle.Migration{Device: e.ID.ID, Phase: "queued"}
			if err != nil {
				m.Phase = "blocked"
				m.Reason = "certificate issuer evidence unavailable"
			}
			if err = a.Certificates.SaveMigration(ctx, job.IssuerID, job.To, m); err != nil {
				return wrapError(err)
			}
		}
	}
	if !remaining {
		return wrapError(a.Certificates.CompleteRollover(ctx, job.IssuerID, job.To))
	}
	return nil
}

func stableUUID(parts string) string {
	sum := sha256.Sum256([]byte(parts))
	s := hex.EncodeToString(sum[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

//nolint:gocyclo // Keep the ordered workflow transitions and their failure handling together.
func (a *App) progressMigration(
	ctx context.Context,
	target *managedIssuerService,
	e *storage.Enrollment,
	m *lifecycle.Migration,
	trustRequired bool,
) error {
	now := a.cfg.Clock.Now()
	m.NextAttempt = now.Add(time.Minute)
	evidence, err := a.issuedIdentity(ctx, e.CertHash)
	if err != nil {
		m.Phase = "blocked"
		m.Reason = "certificate issuer evidence unavailable"
		return nil
	}
	if trustRequired && evidence.Issuer == cms.Fingerprint(target.enrollment.caCert) {
		m.Phase = "confirmed"
		m.Candidate = e.CertHash
		m.Reason = ""
		return nil
	}
	if m.Attempt != "" {
		x, err := a.replacementStore().
			TransitionReplacement(ctx, e.ID, storage.ReplacementChange{Op: "read", At: now})
		if err != nil {
			return wrapError(err)
		}
		if x != nil && x.ID == m.Attempt {
			m.Candidate = x.CandidateHash
			switch x.State {
			case storage.ReplacementCommitted:
				m.Phase = "confirmed"
				m.Reason = ""
				return nil
			case storage.ReplacementPending:
				m.Phase = "identity-pending"
				m.NextAttempt = now.Add(5 * time.Minute)
				if a.Push != nil {
					_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{e.ID})
				}
				return nil
			case storage.ReplacementFailed, storage.ReplacementCancelled:
				m.Phase = "blocked"
				m.Reason = "replacement failed or was cancelled; inspect device and retry explicitly"
				return nil
			case storage.ReplacementExpired:
				m.Attempt = ""
			}
		}
	}
	if now.Sub(e.LastSeenAt) > 5*time.Minute {
		if a.Push != nil {
			_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{e.ID})
		}
		m.Reason = "waiting for device check-in"
		m.NextAttempt = now.Add(time.Hour)
		return nil
	}
	if trustRequired {
		commandID := stableUUID(
			"issuer-trust/" + a.cfg.Setup.IssuerID + "/" + target.enrollment.issuerRevision + "/" + e.ID.ID + fmt.Sprintf(
				"/%d",
				m.Retry,
			),
		)
		command, err := a.queuedCommand(ctx, e.ID, commandID)
		if errors.Is(err, storage.ErrNotFound) {
			p := &profile.Profile{
				Identifier:  "com.deploymenttheory.mdm.issuer-trust",
				UUID:        commandID,
				DisplayName: "MDM enrollment authority trust",
				Scope:       profile.ScopeSystem,
			}
			pairs, err := a.managedIssuerPairs(ctx)
			if err != nil {
				return wrapError(err)
			}
			for _, pair := range pairs {
				hash := cms.Fingerprint(pair.Leaf)
				p.Payloads = append(
					p.Payloads,
					profile.Payload{
						Identifier: p.Identifier + "." + hash,
						UUID:       stableUUID(hash),
						Content:    &profiles.CertificateRoot{PayloadContent: pair.Leaf.Raw},
					},
				)
			}
			raw, err := p.Marshal()
			if err != nil {
				return wrapError(err)
			}
			cmd, err := mdm.NewCommand(
				&commands.InstallProfile{Payload: raw},
				mdm.WithUUID(commandID),
			)
			if err != nil {
				return wrapError(err)
			}
			result, err := a.Core.Enqueue(
				ctx,
				[]mdm.EnrollmentID{e.ID},
				cmd,
				storage.EnqueueOptions{DedupeKey: commandID, Now: now},
			)
			if err != nil {
				return wrapError(err)
			}
			if len(result.Queued) == 0 {
				m.Phase = "blocked"
				m.Reason = "device cannot install the replacement trust profile"
				return nil
			}
			m.TrustCommand = commandID
			m.Phase = "trust-pending"
			if a.Push != nil {
				_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{e.ID})
			}
			return nil
		}
		if err != nil {
			return wrapError(err)
		}
		m.TrustCommand = commandID
		if command.State == storage.StateError || command.State == storage.StateCleared {
			m.Phase = "blocked"
			m.Reason = "trust installation failed or was cleared"
			return nil
		}
		if command.State != storage.StateAcknowledged {
			m.Phase = "trust-pending"
			m.NextAttempt = now.Add(5 * time.Minute)
			if a.Push != nil {
				_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{e.ID})
			}
			return nil
		}
	}
	x, err := a.replacementStore().
		TransitionReplacement(ctx, e.ID, storage.ReplacementChange{Op: "read", At: now})
	if err != nil {
		return wrapError(err)
	}
	if x != nil && x.State == storage.ReplacementPending {
		if x.Issuer != cms.Fingerprint(target.enrollment.caCert) {
			m.Reason = "another identity replacement is in progress"
			return nil
		}
	} else {
		method := evidence.Method
		if method != "acme" && method != "scep" {
			m.Phase = "blocked"
			m.Reason = "identity method requires operator selection"
			return nil
		}
		ctx = context.WithValue(ctx, targetIssuerKey{}, target.enrollment.issuerRevision)
		x, err = a.prepareReplacement(ctx, e.ID, method)
		if err != nil {
			m.Phase = "blocked"
			m.Reason = "enrollment profile is not eligible for automatic replacement"
			return nil
		}
		x, err = a.replacementStore().
			TransitionReplacement(ctx, e.ID, storage.ReplacementChange{Op: "begin", Begin: x, At: now})
		if err != nil {
			return wrapError(err)
		}
	}
	m.Attempt = x.ID
	m.Phase = "identity-pending"
	m.Reason = ""
	if a.Push != nil {
		_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{e.ID})
	}
	return nil
}

func (a *App) queuedCommand(
	ctx context.Context,
	id mdm.EnrollmentID,
	uuid string,
) (storage.QueuedCommand, error) {
	p := paging.Page{Limit: 100}
	for {
		rows, err := a.Store.Commands(ctx, id, storage.CommandQuery{}, p)
		if err != nil {
			return storage.QueuedCommand{}, wrapError(err)
		}
		for _, row := range rows.Items {
			if row.Command.UUID == uuid {
				return row, nil
			}
		}
		if rows.NextCursor == "" {
			return storage.QueuedCommand{}, storage.ErrNotFound
		}
		p.Cursor = rows.NextCursor
	}
}

type deviceRenewal struct {
	OldCertificate string `json:"oldCertificate"`
	lifecycle.Migration
}

func (a *App) renewOneIdentity(ctx context.Context, e storage.Enrollment) error {
	k := "pki/lifecycle/device-renewal/" + digest([]byte(e.ID.ID))
	var job deviceRenewal
	err := a.protocol.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if err == nil {
			if err = json.Unmarshal(r.Value, &job); err != nil {
				return wrapError(err)
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			return wrapError(err)
		}
		if job.OldCertificate == "" ||
			(job.OldCertificate != e.CertHash && job.Phase == "confirmed") {
			job = deviceRenewal{
				OldCertificate: e.CertHash,
				Migration:      lifecycle.Migration{Device: e.ID.ID, Phase: "queued"},
			}
		}
		if job.Phase == "blocked" || tx.Now().Before(job.NextAttempt) {
			return lifecycle.ErrConflict
		}
		job.NextAttempt = tx.Now().Add(5 * time.Minute)
		job.Generation++
		b, err := json.Marshal(job)
		if err != nil {
			return wrapError(err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: b})
	})
	if err != nil {
		return wrapError(err)
	}
	target, err := a.profileIssuer(ctx)
	if err != nil {
		return wrapError(err)
	}
	if err = a.progressMigration(ctx, target, &e, &job.Migration, false); err != nil {
		job.Reason = "identity renewal will retry"
		job.NextAttempt = a.cfg.Clock.Now().Add(time.Hour)
	}
	return wrapError(a.protocol.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if err != nil {
			return wrapError(err)
		}
		var current deviceRenewal
		if err = json.Unmarshal(r.Value, &current); err != nil {
			return wrapError(err)
		}
		if current.Generation != job.Generation {
			return lifecycle.ErrConflict
		}
		b, err := json.Marshal(job)
		if err != nil {
			return wrapError(err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: b})
	}))
}

func (a *App) retireManagedIssuer(ctx context.Context, id, rev string) (lifecycle.Identity, error) {
	if id != a.cfg.Setup.IssuerID {
		return lifecycle.Identity{}, lifecycle.ErrConflict
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	current, err := a.Certificates.Get(ctx, id)
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	var oldHash string
	for _, v := range current.Revisions {
		if v.ID == rev {
			oldHash = v.Fingerprint
		}
	}
	for _, e := range devices {
		evidence, err := a.issuedIdentity(ctx, e.CertHash)
		if err != nil || evidence.Issuer == oldHash {
			return lifecycle.Identity{}, fmt.Errorf(
				"%w: an enabled device still depends on this issuer",
				lifecycle.ErrConflict,
			)
		}
	}
	result, err := a.Certificates.RetireIssuer(ctx, id, rev)
	return result, wrapError(err)
}

func (a *App) reconcileDeviceRenewals(ctx context.Context) error {
	const prefix = "pki/lifecycle/device-renewal/"
	after := ""
	for {
		rows, err := a.protocol.List(ctx, prefix, after, 100)
		if err != nil {
			return wrapError(err)
		}
		for _, row := range rows {
			after = row.Key
			var job deviceRenewal
			if err := json.Unmarshal(row.Value, &job); err != nil {
				return wrapError(err)
			}
			if job.Phase == "confirmed" || job.Phase == "disabled" {
				continue
			}
			e, err := a.Store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: job.Device})
			if errors.Is(err, storage.ErrNotFound) || (err == nil && !e.Enabled) {
				continue
			}
			if err != nil {
				return wrapError(err)
			}
			if err := a.renewOneIdentity(
				ctx,
				*e,
			); err != nil &&
				!errors.Is(err, lifecycle.ErrConflict) {
				return wrapError(err)
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (a *App) deviceRenewalStatus(ctx context.Context, device string) (lifecycle.Migration, error) {
	k := "pki/lifecycle/device-renewal/" + digest([]byte(device))
	row, err := a.protocol.Get(ctx, k)
	if err != nil {
		return lifecycle.Migration{}, wrapError(err)
	}
	var job deviceRenewal
	err = json.Unmarshal(row.Value, &job)
	return job.Migration, wrapError(err)
}

func resetMigration(m *lifecycle.Migration) error {
	if m.Phase == "confirmed" || m.Phase == "disabled" {
		return lifecycle.ErrConflict
	}
	m.Phase, m.Reason = "queued", ""
	m.NextAttempt = time.Time{}
	m.Attempt, m.TrustCommand = "", ""
	m.Retry++
	return nil
}

func (a *App) retryMigration(ctx context.Context, id, rev, device string) error {
	if device == "" || (id != a.cfg.Setup.IssuerID && id != a.cfg.Setup.HTTPSCAID) {
		return lifecycle.ErrInvalid
	}
	if rev != "" {
		cursor := ""
		for {
			rows, next, err := a.Certificates.Migrations(ctx, id, rev, cursor, 100)
			if err != nil {
				return wrapError(err)
			}
			for _, m := range rows {
				if m.Device != device {
					continue
				}
				if err := resetMigration(&m); err != nil {
					return wrapError(err)
				}
				return wrapError(a.Certificates.SaveMigration(ctx, id, rev, m))
			}
			if next == "" {
				return lifecycle.ErrNotFound
			}
			cursor = next
		}
	}
	k := "pki/lifecycle/device-renewal/" + digest([]byte(device))
	return wrapError(a.protocol.Update(ctx, []string{k}, func(tx state.Tx) error {
		row, err := tx.Get(ctx, k)
		if err != nil {
			return wrapError(err)
		}
		var job deviceRenewal
		if err = json.Unmarshal(row.Value, &job); err != nil {
			return wrapError(err)
		}
		if err = resetMigration(&job.Migration); err != nil {
			return wrapError(err)
		}
		job.Generation++
		job.UpdatedAt = tx.Now()
		row.Value, err = json.Marshal(job)
		if err != nil {
			return wrapError(err)
		}
		return tx.Put(ctx, row)
	}))
}
