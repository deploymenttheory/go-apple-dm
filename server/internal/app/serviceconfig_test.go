package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestServiceConfigTrustIsIndependentFromIdentity(t *testing.T) {
	ca, _ := testpki.NewCA("HTTPS CA")
	identity, _ := testpki.NewCA("identity CA")
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			e := &enrollment{
				admission: allowTestAdmission,
				now:       time.Now,
				state:     state.NewMemory(),
				cfg:       EnrollConfig{SCEPChallenge: "secret", Topic: "com.apple.mgmt.test"},
				base:      "https://mdm.example",
				caCert:    identity.Cert,
			}
			if private {
				file := filepath.Join(t.TempDir(), "ca.pem")
				data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
				if err := os.WriteFile(file, append(data, data...), 0o600); err != nil {
					t.Fatal(err)
				}
				e.cfg.TLSAnchorFile = file
			}
			a := &App{cfg: Config{DEP: DEPConfig{ProfileURL: "https://ade.example/enroll"}}}
			mux := http.NewServeMux()
			if err := a.wireServiceConfig(e, mux); err != nil {
				t.Fatal(err)
			}
			get := func(path string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				return w
			}
			w := get(PathServiceConfig)
			var config map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 ||
				w.Header().Get("Content-Type") != "application/json; charset=UTF8" ||
				config["dep_enrollment_url"] != "https://ade.example/enroll" ||
				config["dep_anchor_certs_url"] != "https://mdm.example"+PathTrustAnchors {
				t.Fatal(config, w.Code)
			}
			var anchors [][]byte
			if err := json.Unmarshal(get(PathTrustAnchors).Body.Bytes(), &anchors); err != nil {
				t.Fatal(err)
			}
			p, err := e.profile(t.Context(), acme.Binding{UDID: "mac", CommonName: "mac"})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.ServerCapabilities) != 2 ||
				p.ServerCapabilities[0] != enroll.CapabilityPerUserConnections || p.ServerCapabilities[1] != enroll.CapabilityBootstrapToken {
				t.Fatal("macOS capability missing")
			}
			if private {
				if len(anchors) != 1 || string(anchors[0]) != string(ca.Cert.Raw) ||
					len(p.Roots) != 1 ||
					p.Roots[0].Subject.CommonName != "HTTPS CA" {
					t.Fatal("identity issuer substituted for HTTPS trust")
				}
				parsed, err := profile.Parse(
					get(PathTrustProfile).Body.Bytes(),
					profile.ParseOptions{},
				)
				if err != nil {
					t.Fatal(err)
				}
				if len(parsed.Profile.Payloads) != 1 {
					t.Fatal("unexpected trust payloads")
				}
				if _, ok := parsed.Profile.Payloads[0].Content.(*profiles.CertificateRoot); !ok {
					t.Fatal("non-root trust payload")
				}
			} else if len(anchors) != 0 || len(p.Roots) != 0 || config["trust_profile_url"] != "" || get(PathTrustProfile).Code != 404 {
				t.Fatal("public HTTPS advertises unnecessary trust")
			}
			bad := httptest.NewRecorder()
			mux.ServeHTTP(bad, httptest.NewRequest("POST", PathServiceConfig, nil))
			if bad.Code != 405 {
				t.Fatal("write method accepted")
			}
		})
	}
}

func TestServiceConfigRejectsInvalidTrustAndURL(t *testing.T) {
	ca, _ := testpki.NewCA("ca")
	leaf, _ := ca.Issue("leaf", time.Now().Add(-time.Minute))
	f := filepath.Join(t.TempDir(), "leaf.pem")
	_ = os.WriteFile(
		f,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Cert.Raw}),
		0o600,
	)
	for _, tc := range []struct{ anchor, url string }{{"missing", "https://mdm.example"}, {f, "https://mdm.example"}, {"", "http://mdm.example"}, {"", "https://user:secret@mdm.example"}, {"", "https://"}, {"", ":bad"}} {
		a := &App{}
		e := &enrollment{base: tc.url, cfg: EnrollConfig{TLSAnchorFile: tc.anchor}}
		if err := a.wireServiceConfig(e, http.NewServeMux()); err == nil {
			t.Fatal("invalid discovery accepted", tc)
		}
	}
	e := &enrollment{base: "https://mdm.example"}
	mux := http.NewServeMux()
	if err := (&App{}).wireServiceConfig(e, mux); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", PathServiceConfig, nil))
	var m map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m["dep_enrollment_url"] != e.base+PathADE {
		t.Fatal(m)
	}
}

func TestProfileMetadataPersistsAndRedactsAuthorization(t *testing.T) {
	ca, _ := testpki.NewCA("ca")
	st := state.NewMemory()
	e := &enrollment{
		admission: allowTestAdmission,
		now:       time.Now,
		cfg:       EnrollConfig{SCEPChallenge: "secret", Topic: "com.apple.mgmt.test"},
		base:      "https://mdm.example",
		trust:     []*x509.Certificate{ca.Cert},
		state:     st,
	}
	b := acme.Binding{UDID: "mac", CommonName: "mac"}
	p, err := e.profile(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.recordProfile(context.Background(), b, p); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the enrollment service over retained state, as after a restart.
	restarted := *e
	q, err := restarted.profile(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID != q.UUID || p.MDMUUID != q.MDMUUID || p.IdentityUUID != q.IdentityUUID ||
		p.RootUUIDs[0] != q.RootUUIDs[0] {
		t.Fatal("profile UUIDs changed")
	}
	rec, _ := st.Get(context.Background(), profileMetadataKey(b, p.Identifier))
	var m profileMetadata
	_ = json.Unmarshal(rec.Value, &m)
	template, err := enroll.Parse(m.Template, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if template.SCEP.Challenge != "" {
		t.Fatal("template retained issuance secret")
	}
	if _, err := e.profileWithIdentity(t.Context(), b, "invalid"); err == nil {
		t.Fatal("invalid identity accepted")
	}
	e.admission = nil
	if _, err := e.profileWithIdentity(t.Context(), b, "scep"); err == nil {
		t.Fatal("missing admission accepted")
	}
	if profileMetadataKey(
		acme.Binding{Serial: "s"},
		"i",
	) == profileMetadataKey(
		acme.Binding{CommonName: "c"},
		"i",
	) {
		t.Fatal("profile bindings collide")
	}
}

func allowTestAdmission(context.Context, AdmissionRequest) (AdmissionGrant, error) {
	return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
}
