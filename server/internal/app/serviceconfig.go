package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const (
	PathServiceConfig = "/MDMServiceConfig"
	PathTrustAnchors  = "/enroll/trust/anchors"
	PathTrustProfile  = "/enroll/trust/profile"
)

// wireServiceConfig loads enrollment trust material and mounts the configured
// service-configuration and trust-profile endpoints.
func (a *App) wireServiceConfig(ctx context.Context, e *enrollment, mux *http.ServeMux) error {
	if a.Certificates != nil && a.cfg.Setup.HTTPSCAID != "" {
		material, err := a.Certificates.LoadMaterial(ctx, a.cfg.Setup.HTTPSCAID, "")
		if err == nil {
			rest := material.Certificate
			for len(rest) > 0 {
				block, next := pem.Decode(rest)
				if block == nil {
					break
				}
				c, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return wrapError(err)
				}
				e.trust = append(e.trust, c)
				rest = next
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			return wrapError(err)
		}
	}
	if e.cfg.TLSAnchorFile != "" {
		certs, err := readCertsPEM(e.cfg.TLSAnchorFile)
		if err != nil {
			return fmt.Errorf("%w: HTTPS trust anchors: %w", ErrConfig, err)
		}
		seen := map[string]bool{}
		for _, c := range certs {
			if !c.IsCA || !c.BasicConstraintsValid {
				return fmt.Errorf("%w: HTTPS trust bundle must contain CA certificates", ErrConfig)
			}
			fingerprint := cms.Fingerprint(c)
			if !seen[fingerprint] {
				e.trust = append(e.trust, c)
				seen[fingerprint] = true
			}
		}
	}
	adeURL := a.cfg.DEP.ProfileURL
	if adeURL == "" {
		adeURL = e.base + PathADE
	}
	for _, raw := range []string{adeURL, e.base} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" ||
			u.RawQuery != "" {
			return fmt.Errorf("%w: enrollment discovery requires absolute HTTPS URLs", ErrConfig)
		}
	}
	anchors := make([]string, 0, len(e.trust))
	for _, c := range e.trust {
		anchors = append(anchors, base64.StdEncoding.EncodeToString(c.Raw))
	}
	config := map[string]string{
		"dep_enrollment_url":   adeURL,
		"dep_anchor_certs_url": e.base + PathTrustAnchors,
	}
	if len(e.trust) > 0 {
		config["trust_profile_url"] = e.base + PathTrustProfile
	}
	mux.HandleFunc("GET "+PathServiceConfig, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF8")
		_ = json.NewEncoder(w).Encode(config)
	})
	mux.HandleFunc("GET "+PathTrustAnchors, func(w http.ResponseWriter, r *http.Request) {
		current, err := a.currentTrustAnchors(r.Context(), anchors)
		if err != nil {
			http.Error(w, "trust unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF8")
		_ = json.NewEncoder(w).Encode(current)
	})
	if len(e.trust) > 0 {
		b, err := trustProfile(e.trust)
		if err != nil {
			return wrapError(err)
		}
		mux.HandleFunc("GET "+PathTrustProfile, func(w http.ResponseWriter, r *http.Request) {
			data := b
			if a.Certificates != nil {
				var err error
				data, err = a.SetupTrustProfile(r.Context())
				if err != nil {
					http.Error(w, "trust unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			w.Header().Set("Content-Type", "application/x-apple-aspen-config")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(data)
		})
	}
	return nil
}

// trustProfile encodes the supplied root certificates as a system-scoped configuration
// profile.
func trustProfile(certs []*x509.Certificate) ([]byte, error) {
	p := &profile.Profile{
		Identifier:  "com.deploymenttheory.mdm.https-trust",
		UUID:        profile.NewUUID(),
		DisplayName: "MDM HTTPS trust",
		Scope:       profile.ScopeSystem,
	}
	for _, c := range certs {
		p.Payloads = append(p.Payloads, profile.Payload{
			Identifier: p.Identifier + "." + cms.Fingerprint(c), UUID: profile.NewUUID(),
			DisplayName: c.Subject.CommonName,
			Content:     &profiles.CertificateRoot{PayloadContent: c.Raw},
		})
	}
	b, err := p.Marshal()
	if err != nil {
		return nil, fmt.Errorf("trust profile: %w", err)
	}
	return b, nil
}

type profileMetadata struct {
	Template     []byte            `json:"template"`
	UUID         string            `json:"uuid"`
	MDMUUID      string            `json:"mdmUuid"`
	IdentityUUID string            `json:"identityUuid"`
	Roots        map[string]string `json:"roots"`
}

// profileMetadataKey derives a stable key from the profile identifier and the binding's
// best available device identity.
func profileMetadataKey(b acme.Binding, identifier string) string {
	id := b.EnrollmentUDID()
	if id == "" {
		id = b.Serial
	}
	if id == "" {
		id = b.CommonName
	}
	h := sha256.Sum256([]byte(identifier + "\x00" + id))
	return "enrollment-profile:" + hex.EncodeToString(h[:])
}

// stabilizeProfile atomically reuses persisted profile and payload UUIDs, adding stable
// UUIDs for newly included roots.
func (e *enrollment) stabilizeProfile(
	ctx context.Context,
	b acme.Binding,
	p *enroll.Profile,
) error {
	if e.state == nil {
		return nil
	}
	k := profileMetadataKey(b, p.Identifier)
	err := e.state.Update(ctx, []string{k}, func(tx state.Tx) error {
		m := profileMetadata{
			UUID:         profile.NewUUID(),
			MDMUUID:      profile.NewUUID(),
			IdentityUUID: profile.NewUUID(),
			Roots:        map[string]string{},
		}
		r, err := tx.Get(ctx, k)
		if err == nil {
			if err = json.Unmarshal(r.Value, &m); err != nil {
				return wrapError(err)
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			return wrapError(err)
		}
		p.UUID, p.MDMUUID, p.IdentityUUID = m.UUID, m.MDMUUID, m.IdentityUUID
		for _, c := range p.Roots {
			f := cms.Fingerprint(c)
			if m.Roots[f] == "" {
				m.Roots[f] = profile.NewUUID()
			}
			p.RootUUIDs = append(p.RootUUIDs, m.Roots[f])
		}
		data, err := json.Marshal(m)
		if err != nil {
			return wrapError(err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: data})
	})
	if err != nil {
		return fmt.Errorf("enrollment profile metadata: %w", err)
	}
	return nil
}

// currentTrustAnchors returns current managed trust certificates as base64 DER, using the
// supplied fallback only for static configuration.
func (a *App) currentTrustAnchors(ctx context.Context, fallback []string) ([]string, error) {
	if a.Certificates == nil {
		return fallback, nil
	}
	certificates, err := a.managedProfileTrust(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(certificates))
	for _, certificate := range certificates {
		out = append(out, base64.StdEncoding.EncodeToString(certificate.Raw))
	}
	return out, nil
}
