package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

const replacementSubjectPrefix = "dm-replace:"

type identityEvidence struct {
	Method   string    `json:"method"`
	NotAfter time.Time `json:"notAfter"`
}

func (a *App) recordIssuedIdentity(ctx context.Context, c *x509.Certificate) error {
	method := revocation.ProvenanceFromContext(ctx).Source
	if method == "" {
		method = IdentitySCEP
	}
	b, err := json.Marshal(identityEvidence{Method: method, NotAfter: c.NotAfter})
	if err != nil {
		return fmt.Errorf("app: identity evidence: %w", err)
	}
	k := "issued-identity:" + cms.Fingerprint(c)
	err = a.protocol.Update(ctx, []string{k}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: k, Value: b, ExpiresAt: c.NotAfter})
	})
	if err != nil {
		return fmt.Errorf("app: record identity evidence: %w", err)
	}
	return nil
}

func (a *App) enrollmentEvidence(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	e, err := a.Store.Get(r.Context(), id)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	var evidence identityEvidence
	if e.CertHash != "" {
		rec, err := a.protocol.Get(r.Context(), "issued-identity:"+e.CertHash)
		if err == nil {
			if err = json.Unmarshal(rec.Value, &evidence); err != nil {
				writeError(w, 500, errOperation)
				return
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			writeError(w, 500, errOperation)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(
		w,
		200,
		map[string]any{
			"ID":              e.ID.ID,
			"Enabled":         e.Enabled,
			"Identity":        evidence.Method,
			"Certificate":     e.CertHash,
			"NotAfter":        evidence.NotAfter,
			"AuthenticatedAt": e.EnrolledAt,
			"TokenUpdatedAt":  e.TokenUpdatedAt,
		},
	)
}

type replacementAdmission struct{ app *App }

func (h replacementAdmission) Before(
	ctx context.Context,
	call *service.Call,
) (context.Context, error) {
	if call.Request == nil || call.Request.Certificate == nil ||
		!strings.HasPrefix(call.Request.Certificate.Subject.CommonName, replacementSubjectPrefix) {
		return ctx, nil
	}
	id, attempt, err := parseReplacementSubject(call.Request.Certificate.Subject.CommonName)
	if err != nil || id != call.Request.ID.Device() {
		return ctx, fmt.Errorf(
			"%w: replacement identity belongs to another device",
			storage.ErrConflict,
		)
	}
	hash := cms.Fingerprint(call.Request.Certificate)
	pin, err := h.app.Store.CertHash(ctx, id)
	if err != nil {
		return ctx, wrapError(err)
	}
	if pin == hash {
		return ctx, nil
	}
	s := h.app.replacementStore()
	if s == nil {
		return ctx, storage.ErrInvalid
	}
	x, err := s.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{Op: "read", At: h.app.cfg.Clock.Now()},
	)
	if err != nil {
		return ctx, wrapError(err)
	}
	if x == nil || x.ID != attempt || x.CandidateHash != hash ||
		x.State != storage.ReplacementPending {
		return ctx, fmt.Errorf("%w: unauthorized replacement identity", storage.ErrConflict)
	}
	return ctx, nil
}

func (replacementAdmission) After(context.Context, *service.Call, error) {}

func (a *App) replacementStore() storage.ReplacementStore {
	s, _ := a.Store.(storage.ReplacementStore)
	return s
}

func replacementSubject(id mdm.EnrollmentID, attempt string) string {
	return replacementSubjectPrefix + base64.RawURLEncoding.EncodeToString(
		[]byte(id.ID),
	) + ":" + attempt
}

func parseReplacementSubject(subject string) (mdm.EnrollmentID, string, error) {
	encoded, attempt, ok := strings.Cut(strings.TrimPrefix(subject, replacementSubjectPrefix), ":")
	id, err := base64.RawURLEncoding.DecodeString(encoded)
	device := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: string(id)}
	if !strings.HasPrefix(subject, replacementSubjectPrefix) || !ok || attempt == "" ||
		err != nil ||
		device.Validate() != nil {
		return mdm.EnrollmentID{}, "", fmt.Errorf(
			"%w: malformed replacement subject",
			storage.ErrInvalid,
		)
	}
	return device, attempt, nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (a *App) replacementIssuance(ctx context.Context, cert *x509.Certificate) error {
	if !strings.HasPrefix(cert.Subject.CommonName, replacementSubjectPrefix) {
		return nil
	}
	id, attempt, err := parseReplacementSubject(cert.Subject.CommonName)
	if err != nil {
		return wrapError(err)
	}
	s := a.replacementStore()
	if s == nil {
		return fmt.Errorf("%w: replacement storage unavailable", storage.ErrInvalid)
	}
	method := revocation.ProvenanceFromContext(ctx).Source
	if method == "" {
		method = IdentitySCEP
	}
	_, err = s.TransitionReplacement(ctx, id, storage.ReplacementChange{
		Op: "issue", ID: attempt, Hash: cms.Fingerprint(cert), Method: method,
		PublicKeyHash: digest(cert.RawSubjectPublicKeyInfo), At: a.cfg.Clock.Now(),
	})
	return wrapError(err)
}

func (a *App) replacementChallenge(
	ctx context.Context,
	password string,
	csr *x509.CertificateRequest,
) error {
	id, attempt, err := parseReplacementSubject(csr.Subject.CommonName)
	if err != nil {
		return wrapError(err)
	}
	s := a.replacementStore()
	if s == nil {
		return fmt.Errorf("%w: replacement storage unavailable", storage.ErrInvalid)
	}
	_, err = s.TransitionReplacement(ctx, id, storage.ReplacementChange{
		Op:            "claim",
		ID:            attempt,
		SecretHash:    digest([]byte(password)),
		PublicKeyHash: digest(csr.RawSubjectPublicKeyInfo),
		At:            a.cfg.Clock.Now(),
	})
	return wrapError(err)
}

func (a *App) replaceEnrollment(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	s := a.replacementStore()
	if s == nil {
		writeError(
			w,
			http.StatusServiceUnavailable,
			fmt.Errorf("%w: replacement storage unavailable", errOperation),
		)
		return
	}
	ch := storage.ReplacementChange{Op: "read", At: a.cfg.Clock.Now()}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Identity string `json:"identity"`
		}
		body, readErr := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if readErr != nil || len(body) > MaxAdminBody {
			writeError(w, 413, ErrBodyTooLarge)
			return
		}
		if len(body) > 0 && json.Unmarshal(body, &req) != nil {
			writeError(w, 400, ErrBadACMERequest)
			return
		}
		x, err := a.prepareReplacement(r.Context(), id, req.Identity)
		if err != nil {
			a.storageStatus(w, r, err)
			return
		}
		ch.Op, ch.Begin = "begin", x
	case http.MethodDelete:
		ch.Op, ch.ID = "cancel", r.PathValue("attempt")
	}
	ch.At = a.cfg.Clock.Now()
	x, err := s.TransitionReplacement(r.Context(), id, ch)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	if x == nil {
		a.storageStatus(w, r, storage.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"ID":                   x.ID,
		"Identity":             x.Method,
		"State":                x.State,
		"ExpiresAt":            x.ExpiresAt,
		"CompletedAt":          x.CompletedAt,
		"Delivered":            x.Delivered,
		"Acknowledged":         x.Acknowledged,
		"Authenticated":        x.Authenticated,
		"OldCertificate":       x.OldHash,
		"CandidateCertificate": x.CandidateHash,
	})
}

func (a *App) prepareReplacement(
	ctx context.Context,
	id mdm.EnrollmentID,
	method string,
) (*storage.Replacement, error) {
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		return nil, wrapError(err)
	}
	if id.Channel.IsUser() || !e.Enabled || e.CertHash == "" {
		return nil, fmt.Errorf("%w: replacement needs an enabled device", storage.ErrConflict)
	}
	if method == "" {
		method = a.enroll.cfg.Identity
	}
	if method == "" {
		method = IdentitySCEP
	}
	attempt := profile.NewUUID()
	b := acme.Binding{
		UDID:       id.ID,
		Serial:     e.Device.SerialNumber,
		CommonName: replacementSubject(id, attempt),
	}
	fresh, err := a.enroll.profileWithIdentity(ctx, b, method)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	k := profileMetadataKey(b, fresh.Identifier)
	rec, err := a.enroll.state.Get(ctx, k)
	if err != nil {
		return nil, fmt.Errorf("%w: enrollment profile metadata unavailable", storage.ErrConflict)
	}
	var metadata profileMetadata
	if err := json.Unmarshal(rec.Value, &metadata); err != nil {
		return nil, fmt.Errorf("app: read profile metadata: %w", err)
	}
	if len(metadata.Template) == 0 {
		return nil, fmt.Errorf(
			"%w: original enrollment profile must be recorded before replacement",
			storage.ErrConflict,
		)
	}
	p, err := enroll.Parse(metadata.Template, profile.ParseOptions{})
	if err != nil {
		return nil, fmt.Errorf("app: original profile: %w", err)
	}
	if p.AccessRights&enroll.RightInstallProfiles == 0 {
		return nil, fmt.Errorf(
			"%w: original profile lacks profile-installation rights",
			storage.ErrConflict,
		)
	}
	if p.Topic != fresh.Topic || p.ServerURL != fresh.ServerURL ||
		p.CheckInURL != fresh.CheckInURL {
		return nil, fmt.Errorf(
			"%w: replacement cannot change the management endpoint or topic",
			storage.ErrConflict,
		)
	}
	p.SCEP, p.ACME, p.PKCS12, p.IdentityUUID = fresh.SCEP, fresh.ACME, nil, profile.NewUUID()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("app: replacement authorization: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	if p.SCEP != nil {
		p.SCEP.Challenge = password
	}
	raw, err := p.Marshal()
	if err != nil {
		return nil, fmt.Errorf("app: replacement profile: %w", err)
	}
	cmd, err := mdm.NewCommand(&commands.InstallProfile{Payload: raw}, mdm.WithUUID(attempt))
	if err != nil {
		return nil, fmt.Errorf("app: replacement command: %w", err)
	}
	cmd.Payload = nil
	return &storage.Replacement{
		ID:         attempt,
		Method:     method,
		OldHash:    e.CertHash,
		SecretHash: digest([]byte(password)),
		Command:    *cmd,
		ExpiresAt:  a.cfg.Clock.Now().Add(30 * time.Minute),
	}, nil
}

func (e *enrollment) recordProfile(
	ctx context.Context,
	binding acme.Binding,
	p *enroll.Profile,
) error {
	if e.state == nil {
		return nil
	}
	copy := *p
	if p.SCEP != nil {
		s := *p.SCEP
		s.Challenge = ""
		copy.SCEP = &s
	}
	if p.ACME != nil {
		ac := *p.ACME
		ac.ClientIdentifier = "redacted"
		copy.ACME = &ac
	}
	raw, err := copy.Marshal()
	if err != nil {
		return fmt.Errorf("app: profile template: %w", err)
	}
	k := profileMetadataKey(binding, p.Identifier)
	err = e.state.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if err != nil {
			return wrapError(err)
		}
		var metadata profileMetadata
		if err := json.Unmarshal(r.Value, &metadata); err != nil {
			return wrapError(err)
		}
		metadata.Template = raw
		r.Value, err = json.Marshal(metadata)
		if err != nil {
			return wrapError(err)
		}
		return tx.Put(ctx, r)
	})
	if err != nil {
		return fmt.Errorf("app: record profile: %w", err)
	}
	return nil
}
