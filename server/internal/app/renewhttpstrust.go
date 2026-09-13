package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// startHTTPSTrustRollover leaves the old HTTPS trust and leaf active until the
// enabled device cohort has acknowledged the replacement trust profile.
func (a *App) startHTTPSTrustRollover(
	ctx context.Context,
	id, rev string,
) (lifecycle.Rollover, error) {
	if a.enroll == nil || id != a.cfg.Setup.HTTPSCAID {
		return lifecycle.Rollover{}, lifecycle.ErrConflict
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return lifecycle.Rollover{}, wrapError(err)
	}
	ids := make([]string, 0, len(devices))
	for _, e := range devices {
		ids = append(ids, e.ID.ID)
	}
	job, err := a.Certificates.PrepareRollover(ctx, id, rev, ids)
	return job, wrapError(err)
}

func (a *App) advanceHTTPSTrust(ctx context.Context, job lifecycle.Rollover) error {
	if job.Phase == "complete" {
		return nil
	}
	pairs, err := a.certificatePairs(ctx, job.IssuerID, false)
	if err != nil {
		return wrapError(err)
	}
	devices, err := a.enabledDevices(ctx)
	if err != nil {
		return wrapError(err)
	}
	known := map[string]bool{}
	remaining := false
	cursor := ""
	for {
		rows, next, err := a.Certificates.Migrations(ctx, job.IssuerID, job.To, cursor, 100)
		if err != nil {
			return wrapError(err)
		}
		for _, m := range rows {
			known[m.Device] = true
			if m.Phase == "confirmed" || m.Phase == "disabled" {
				continue
			}
			remaining = true
			if m.Phase == "blocked" || a.cfg.Clock.Now().Before(m.NextAttempt) {
				continue
			}
			m.NextAttempt = a.cfg.Clock.Now().Add(5 * time.Minute)
			if err := a.Certificates.SaveMigration(
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
			if err := a.progressHTTPSTrust(ctx, job, pairs, &m); err != nil {
				m.Reason = "HTTPS trust installation will retry"
			}
			if err := a.Certificates.SaveMigration(
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
	for _, e := range devices {
		if known[e.ID.ID] {
			continue
		}
		remaining = true
		if err := a.Certificates.SaveMigration(
			ctx,
			job.IssuerID,
			job.To,
			lifecycle.Migration{Device: e.ID.ID, Phase: "queued"},
		); err != nil {
			return wrapError(err)
		}
	}
	if remaining {
		return nil
	}
	return a.activateHTTPSTrust(ctx, job)
}

func (a *App) activateHTTPSTrust(ctx context.Context, job lifecycle.Rollover) error {
	// Every existing enabled device has confirmed trust. New enrollment profiles
	// already include pending HTTPS trust, closing the cohort discovery window.
	if _, err := a.Certificates.ActivateRollover(ctx, job.IssuerID, job.To); err != nil {
		return wrapError(err)
	}
	https, err := a.Certificates.Get(ctx, a.cfg.Setup.HTTPSID)
	if err != nil {
		return wrapError(err)
	}
	// Resume after a crash between CA activation and leaf activation.
	active, err := a.Certificates.LoadMaterial(ctx, https.ID, "")
	if err != nil {
		return wrapError(err)
	}
	ca, err := a.Certificates.LoadMaterial(ctx, job.IssuerID, job.To)
	if err != nil {
		return wrapError(err)
	}
	leaf, err := tls.X509KeyPair(active.Certificate, active.Key)
	if err != nil {
		return wrapError(err)
	}
	issuer, err := tls.X509KeyPair(ca.Certificate, ca.Key)
	if err != nil {
		return wrapError(err)
	}
	if leaf.Leaf.CheckSignatureFrom(issuer.Leaf) != nil {
		if https.Pending != "" {
			prepared, err := a.Certificates.LoadMaterial(ctx, https.ID, https.Pending)
			if err != nil {
				return wrapError(err)
			}
			if len(prepared.Certificate) > 0 {
				candidate, err := tls.X509KeyPair(prepared.Certificate, prepared.Key)
				if err != nil {
					return wrapError(err)
				}
				if candidate.Leaf.CheckSignatureFrom(issuer.Leaf) != nil {
					previous, err := a.Certificates.LoadMaterial(ctx, job.IssuerID, job.From)
					if err != nil {
						return wrapError(err)
					}
					old, err := tls.X509KeyPair(previous.Certificate, previous.Key)
					if err != nil {
						return wrapError(err)
					}
					if candidate.Leaf.CheckSignatureFrom(old.Leaf) != nil {
						return fmt.Errorf(
							"%w: resolve the separately imported pending HTTPS identity",
							lifecycle.ErrConflict,
						)
					}
					if _, err := a.Certificates.Cancel(ctx, https.ID, https.Pending); err != nil {
						return wrapError(err)
					}
				}
			}
		}
		pending, err := a.Certificates.Begin(ctx, https.Request)
		if err != nil {
			return wrapError(err)
		}
		if _, err := a.Certificates.IssueHTTPS(
			ctx,
			https.ID,
			pending.Pending,
			job.IssuerID,
		); err != nil {
			return wrapError(err)
		}
		if _, err := a.ExecuteSetup(
			ctx,
			lifecycle.HTTPS,
			"activate",
			SetupRequest{Request: https.Request, Revision: pending.Pending},
		); err != nil {
			return err
		}
	}
	return wrapError(a.Certificates.CompleteRollover(ctx, job.IssuerID, job.To))
}

func (a *App) progressHTTPSTrust(
	ctx context.Context,
	job lifecycle.Rollover,
	pairs []tls.Certificate,
	m *lifecycle.Migration,
) error {
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: m.Device}
	e, err := a.Store.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !e.Enabled) {
		m.Phase = "disabled"
		return nil
	}
	if err != nil {
		return wrapError(err)
	}
	commandID := stableUUID(
		fmt.Sprintf("https-trust/%s/%s/%s/%d", job.IssuerID, job.To, m.Device, m.Retry),
	)
	command, err := a.queuedCommand(ctx, id, commandID)
	switch {
	case err == nil:
		m.TrustCommand = commandID
		switch command.State {
		case storage.StateAcknowledged:
			m.Phase = "confirmed"
			m.Reason = ""
			return nil
		case storage.StateError, storage.StateCleared:
			m.Phase = "blocked"
			m.Reason = "HTTPS trust installation failed or was cleared"
			return nil
		}
	case !errors.Is(err, storage.ErrNotFound):
		return wrapError(err)
	default:
		p := &profile.Profile{
			Identifier:  "com.deploymenttheory.mdm.https-trust",
			UUID:        stableUUID("managed HTTPS trust"),
			DisplayName: "MDM HTTPS trust",
			Scope:       profile.ScopeSystem,
		}
		for _, pair := range pairs {
			fingerprint := digest(pair.Leaf.Raw)
			p.Payloads = append(
				p.Payloads,
				profile.Payload{
					Identifier: p.Identifier + "." + fingerprint,
					UUID:       stableUUID(fingerprint),
					Content:    &profiles.CertificateRoot{PayloadContent: pair.Leaf.Raw},
				},
			)
		}
		raw, err := p.Marshal()
		if err != nil {
			return wrapError(err)
		}
		cmd, err := mdm.NewCommand(&commands.InstallProfile{Payload: raw}, mdm.WithUUID(commandID))
		if err != nil {
			return wrapError(err)
		}
		result, err := a.Core.Enqueue(
			ctx,
			[]mdm.EnrollmentID{id},
			cmd,
			storage.EnqueueOptions{DedupeKey: commandID, Now: a.cfg.Clock.Now()},
		)
		if err != nil {
			return wrapError(err)
		}
		if len(result.Queued) == 0 {
			m.Phase = "blocked"
			m.Reason = "device cannot install HTTPS trust"
			return nil
		}
		m.TrustCommand = commandID
	}
	m.Phase = "trust-pending"
	m.Reason = "waiting for HTTPS trust acknowledgement"
	if a.Push != nil {
		_, _ = a.Push.Notify(ctx, []mdm.EnrollmentID{id})
	}
	return nil
}

func (a *App) retireHTTPSTrust(ctx context.Context, id, rev string) (lifecycle.Identity, error) {
	material, err := a.Certificates.LoadMaterial(ctx, id, rev)
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	old, err := tls.X509KeyPair(material.Certificate, material.Key)
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	active, err := a.Certificates.LoadMaterial(ctx, a.cfg.Setup.HTTPSID, "")
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	leaf, err := tls.X509KeyPair(active.Certificate, active.Key)
	if err != nil {
		return lifecycle.Identity{}, wrapError(err)
	}
	if leaf.Leaf.CheckSignatureFrom(old.Leaf) == nil {
		return lifecycle.Identity{}, fmt.Errorf(
			"%w: HTTPS still depends on the old trust CA",
			lifecycle.ErrConflict,
		)
	}
	result, err := a.Certificates.RetireIssuer(ctx, id, rev)
	return result, wrapError(err)
}
