package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// SetupConfig selects identities in encrypted persistent storage. It contains
// references and operational policy, never private key material.
type SetupConfig struct {
	HTTP01Listen    string `json:"http01Listen,omitempty"`
	Role            string `json:"role"`
	VendorID        string `json:"vendorId"`
	PushID          string `json:"pushId"`
	HTTPSID         string `json:"httpsId"`
	IssuerID        string `json:"issuerId"`
	HTTPSCAID       string `json:"httpsCaId,omitempty"`
	VendorURL       string `json:"vendorUrl,omitempty"`
	VendorTokenFile string `json:"vendorTokenFile,omitempty"`
}

// OpenSetup opens only storage and certificate management, for local bootstrap.
// The returned App must be closed. It does not start listeners or workers.
func OpenSetup(ctx context.Context, cfg Config) (*App, error) {
	if cfg.Setup == nil {
		return nil, fmt.Errorf("%w: setup configuration required", ErrConfig)
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	a := &App{cfg: cfg}
	if err := a.openStorage(ctx); err != nil {
		_ = a.Close() // Close owns a bounded drain after cancellation.
		return nil, wrapError(err)
	}
	if err := a.openCertificates(ctx); err != nil {
		_ = a.Close() // Close owns a bounded drain after cancellation.
		return nil, wrapError(err)
	}
	return a, nil
}

func (a *App) openCertificates(ctx context.Context) error {
	if err := a.openReplyCertificates(ctx); err != nil {
		return err
	}
	if a.cfg.Setup == nil {
		return nil
	}
	if a.cfg.Setup.Role != "vendor" && a.cfg.Setup.Role != "customer" &&
		a.cfg.Setup.Role != "combined" {
		return fmt.Errorf("%w: setup role must be vendor, customer or combined", ErrConfig)
	}
	if a.cfg.Storage == "sqlite" &&
		(strings.Contains(a.cfg.DSN, ":memory:") || strings.Contains(a.cfg.DSN, "mode=memory")) {
		return fmt.Errorf("%w: managed certificates require a persistent SQLite file", ErrConfig)
	}
	references := []string{
		a.cfg.Setup.HTTPSID,
		a.cfg.Setup.HTTPSCAID,
		a.cfg.Setup.IssuerID,
		a.cfg.Setup.PushID,
		a.cfg.Setup.VendorID,
	}
	seen := map[string]bool{}
	for _, id := range references {
		if id == "" {
			continue
		}
		if seen[id] {
			return fmt.Errorf(
				"%w: managed certificate references must use distinct identity IDs",
				ErrConfig,
			)
		}
		seen[id] = true
	}
	if a.keyring == nil || a.db == nil {
		return fmt.Errorf("%w: managed certificates require encrypted SQL storage", ErrConfig)
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return wrapError(err)
	}
	a.Certificates = &lifecycle.Manager{Store: st, Publish: statestore.PublishCertificate}
	if id := a.cfg.Setup.HTTPSCAID; id != "" {
		material, err := a.Certificates.LoadMaterial(ctx, id, "")
		if err == nil {
			pool, err := x509.SystemCertPool()
			if err != nil {
				return wrapError(err)
			}
			if !pool.AppendCertsFromPEM(material.Certificate) {
				return ErrConfig
			}
			a.Certificates.Trust.HTTPSRoots = pool
		} else if !errors.Is(err, lifecycle.ErrNotFound) {
			return wrapError(err)
		}
	}
	return nil
}

func (a *App) configureManagedIdentities(ctx context.Context) error {
	if a.Certificates == nil {
		return nil
	}
	s := a.cfg.Setup
	if a.cfg.TLSCertFile != "" || a.cfg.TLSKeyFile != "" || a.cfg.Enroll.CACertFile != "" ||
		a.cfg.Enroll.CAKeyFile != "" ||
		a.cfg.Push.Source == PushSourceFile {
		return fmt.Errorf(
			"%w: managed certificate references conflict with file identities",
			ErrConfig,
		)
	}
	if s.Role == "vendor" {
		a.cfg.Enroll.Topic = ""
		return nil
	}
	push, err := a.Certificates.Get(ctx, s.PushID)
	if errors.Is(err, lifecycle.ErrNotFound) {
		a.cfg.Enroll.Topic = ""
		return nil
	}
	if err != nil {
		return wrapError(err)
	}
	issuer, err := a.Certificates.Get(ctx, s.IssuerID)
	if errors.Is(err, lifecycle.ErrNotFound) {
		a.cfg.Enroll.Topic = ""
		return nil
	}
	if err != nil {
		return wrapError(err)
	}
	if push.Active == "" || issuer.Active == "" {
		a.cfg.Enroll.Topic = ""
		return nil
	}
	if a.cfg.Enroll.Topic != "" && a.cfg.Enroll.Topic != push.Topic {
		return fmt.Errorf(
			"%w: configured enrollment topic does not match managed identity",
			ErrConfig,
		)
	}
	a.cfg.Enroll.Topic = push.Topic
	a.cfg.Push.Source = PushSourceStore
	a.cfg.PKI.Enabled = !a.cfg.PKI.Disabled
	if a.cfg.PKI.CRLTTL == 0 {
		a.cfg.PKI.CRLTTL = 24 * time.Hour
	}
	if a.cfg.PKI.CRLRefresh == 0 {
		a.cfg.PKI.CRLRefresh = time.Hour
	}
	if a.cfg.PKI.OCSPTTL == 0 {
		a.cfg.PKI.OCSPTTL = 15 * time.Minute
	}
	return nil
}

// TLSCertificate loads the active revision for each new TLS handshake.
func (a *App) TLSCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	ctx := context.Background()
	if hello != nil {
		ctx = hello.Context()
	}
	return a.LoadTLSCertificate(ctx)
}

// LoadTLSCertificate verifies the active HTTPS identity for startup and probes.
func (a *App) LoadTLSCertificate(ctx context.Context) (*tls.Certificate, error) {
	if a.Certificates == nil {
		return nil, ErrConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	material, err := a.Certificates.LoadMaterial(ctx, a.cfg.Setup.HTTPSID, "")
	if err != nil {
		return nil, wrapError(err)
	}
	pair, err := tls.X509KeyPair(material.Certificate, material.Key)
	if err != nil {
		return nil, wrapError(err)
	}
	if now := a.cfg.Clock.Now(); now.Before(pair.Leaf.NotBefore) ||
		!now.Before(pair.Leaf.NotAfter) {
		return nil, fmt.Errorf("%w: HTTPS identity outside validity interval", ErrConfig)
	}
	return &pair, nil
}

func (a *App) renewCertificates(ctx context.Context) error {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		if err := a.certificateRenewalPass(ctx); err != nil && ctx.Err() == nil {
			a.cfg.Logger.ErrorContext(ctx, "certificate renewal scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (a *App) certificateRenewalPass(ctx context.Context) error {
	ctx = context.WithValue(ctx, setupActorKey{}, "certificate-renewal-worker")
	ctx = lifecycle.WithAudit(ctx, "certificate-renewal-worker", "scheduled-renewal")
	items, err := a.Certificates.List(ctx)
	if err != nil {
		return wrapError(err)
	}
	for _, item := range items {
		if item.Severity != "" {
			if item.Pending == "" {
				item, err = a.Certificates.Begin(ctx, item.Request)
				if err != nil {
					return wrapError(err)
				}
			}
			notice, created, err := a.Certificates.RecordNotice(ctx, item.ID)
			if err != nil {
				return wrapError(err)
			}
			if created {
				a.cfg.Logger.WarnContext(
					ctx,
					"certificate renewal needed",
					"identity",
					notice.Identity,
					"revision",
					notice.Revision,
					"severity",
					notice.Severity,
					"next_action",
					notice.NextAction,
				)
			}
		}
		if item.ID == a.cfg.Setup.HTTPSCAID {
			for _, revision := range item.Revisions {
				job, e := a.Certificates.Rollover(ctx, item.ID, revision.ID)
				if errors.Is(e, lifecycle.ErrNotFound) {
					continue
				}
				if e != nil {
					return wrapError(e)
				}
				if e := a.advanceHTTPSTrust(ctx, job); e != nil {
					return e
				}
			}
		}
		if item.Pending == "" {
			continue
		}
		if err = a.advanceCertificate(
			ctx,
			item,
		); err != nil &&
			!errors.Is(err, lifecycle.ErrConflict) {
			a.cfg.Logger.WarnContext(
				ctx,
				"certificate workflow pending",
				"identity",
				item.ID,
				"error",
				err,
			)
		}
	}
	return nil
}

func (a *App) advanceCertificate(ctx context.Context, item lifecycle.Identity) error {
	switch item.Kind {
	case lifecycle.Push:
		if a.cfg.Setup.Role != "combined" && a.cfg.Setup.VendorURL == "" {
			return nil
		}
		if _, err := a.Certificates.Export(
			ctx,
			item.ID,
			item.Pending,
			"signed-request",
		); err == nil {
			return nil
		}
		_, err := a.ExecuteSetup(
			ctx,
			lifecycle.Push,
			"sign",
			SetupRequest{Request: item.Request, Revision: item.Pending},
		)
		return wrapError(err)
	case lifecycle.HTTPS:
		if _, err := a.Certificates.PublicACMEStatus(ctx, item.ID); err == nil {
			_, err = a.Certificates.RunPublicACME(ctx, item.ID, nil)
			return wrapError(err)
		} else if !errors.Is(err, lifecycle.ErrNotFound) {
			return wrapError(err)
		}
		// Automatically renew only leaves previously signed by the configured lab
		// CA. An imported public certificate remains an explicit import workflow.
		if item.Active == "" {
			return nil
		}
		active, err := a.Certificates.LoadMaterial(ctx, item.ID, item.Active)
		if err != nil {
			return wrapError(err)
		}
		ca, err := a.Certificates.LoadMaterial(ctx, a.cfg.Setup.HTTPSCAID, "")
		if errors.Is(err, lifecycle.ErrNotFound) {
			return nil
		}
		if err != nil {
			return wrapError(err)
		}
		leafBlock, _ := pem.Decode(active.Certificate)
		caBlock, _ := pem.Decode(ca.Certificate)
		if leafBlock == nil || caBlock == nil {
			return lifecycle.ErrInvalid
		}
		leaf, err := x509.ParseCertificate(leafBlock.Bytes)
		if err != nil {
			return wrapError(err)
		}
		parent, err := x509.ParseCertificate(caBlock.Bytes)
		if err != nil {
			return wrapError(err)
		}
		if leaf.CheckSignatureFrom(parent) != nil {
			return nil //nolint:nilerr // A certificate issued by an external CA requires operator signing instead of automatic issuance.
		}
		if _, err = a.Certificates.IssueHTTPS(
			ctx,
			item.ID,
			item.Pending,
			a.cfg.Setup.HTTPSCAID,
		); err != nil {
			return wrapError(err)
		}
		_, err = a.ExecuteSetup(
			ctx,
			lifecycle.HTTPS,
			"activate",
			SetupRequest{Request: item.Request, Revision: item.Pending},
		)
		return wrapError(err)
	case lifecycle.Issuer:
		if (item.ID != a.cfg.Setup.IssuerID && item.ID != a.cfg.Setup.HTTPSCAID) ||
			item.Active == "" ||
			a.enroll == nil {
			return nil
		}
		material, err := a.Certificates.LoadMaterial(ctx, item.ID, item.Active)
		if err != nil {
			return wrapError(err)
		}
		block, _ := pem.Decode(material.Certificate)
		if block == nil {
			return lifecycle.ErrInvalid
		}
		issuer, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return wrapError(err)
		}
		// External CA policy requires an external signing step. Locally managed
		// roots can prepare a successor and begin the tracked trust migration.
		if !bytes.Equal(issuer.RawSubject, issuer.RawIssuer) ||
			issuer.CheckSignatureFrom(issuer) != nil {
			return nil //nolint:nilerr // A certificate issued by an external CA requires operator signing instead of automatic issuance.
		}
		if _, err = a.Certificates.CreateIssuer(ctx, item.ID, item.Pending, 0); err != nil {
			return wrapError(err)
		}
		if item.ID == a.cfg.Setup.HTTPSCAID {
			_, err = a.startHTTPSTrustRollover(ctx, item.ID, item.Pending)
		} else {
			_, err = a.startIssuerRollover(ctx, item.ID, item.Pending)
		}
		return wrapError(err)
	}
	return nil
}
