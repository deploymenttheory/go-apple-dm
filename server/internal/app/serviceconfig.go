package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/state"
)

const (
	PathServiceConfig = "/MDMServiceConfig"
	PathTrustAnchors  = "/enroll/trust/anchors"
	PathTrustProfile  = "/enroll/trust/profile"
)

func (a *App) wireServiceConfig(e *enrollment, mux *http.ServeMux) error {
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
	mux.HandleFunc("GET "+PathTrustAnchors, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF8")
		_ = json.NewEncoder(w).Encode(anchors)
	})
	if len(e.trust) > 0 {
		b, err := trustProfile(e.trust)
		if err != nil {
			return wrapError(err)
		}
		mux.HandleFunc("GET "+PathTrustProfile, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/x-apple-aspen-config")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(b)
		})
	}
	return nil
}

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
		return nil, fmt.Errorf("app: trust profile: %w", err)
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

func profileMetadataKey(b acme.Binding, identifier string) string {
	id := b.UDID
	if id == "" {
		id = b.Serial
	}
	if id == "" {
		id = b.CommonName
	}
	h := sha256.Sum256([]byte(identifier + "\x00" + id))
	return "enrollment-profile:" + hex.EncodeToString(h[:])
}

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
		return fmt.Errorf("app: enrollment profile metadata: %w", err)
	}
	return nil
}
