package app_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	json "encoding/json/v2"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestBlueprintAdminAndDeviceDelivery(t *testing.T) {
	ca, err := testpki.NewCA("blueprint identity")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ca.Issue("device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	other, err := ca.Issue("other", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	p := &profile.Profile{Identifier: "com.example.settings", UUID: "6C9B0C20-0000-7000-8000-000000000001", Payloads: []profile.Payload{{Identifier: "com.example.settings.payload", UUID: "6C9B0C20-0000-7000-8000-000000000002", Content: &profile.Raw{Type: "com.example.custom", Keys: map[string]any{"Value": "profile-secret"}}}}}
	profileData, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			a := build(t, app.Config{Storage: backend, DSN: filepath.Join(t.TempDir(), "app.db"), BootstrapToken: "admin", CARoots: ca.Pool(), Enroll: app.EnrollConfig{PublicURL: "https://mdm.example"}})
			request := func(method, path, revision string, body []byte, cert *x509.Certificate) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequestWithContext(t.Context(), method, "https://mdm.example"+path, bytes.NewReader(body))
				if strings.HasPrefix(path, "/admin/") {
					r.Header.Set("Authorization", "Bearer admin")
				}
				if revision != "" {
					r.Header.Set("If-Match", `"`+revision+`"`)
				}
				if cert != nil {
					r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert, ca.Cert}}}
				}
				w := httptest.NewRecorder()
				a.Handler.ServeHTTP(w, r)
				return w
			}
			w := request("POST", "/admin/v1/configuration-profiles", "", profileData, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var info configurationprofile.Info
			if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			spec := blueprint.Spec{Identifier: "engineering", Declarations: []blueprint.Declaration{{Identifier: "profile", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: info.Revision}}}}
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			w = request("POST", "/admin/v1/blueprints/validate", "", raw, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w = request("PUT", "/admin/v1/blueprints/engineering", "", raw, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var record blueprints.Record
			if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if w.Header().Get("ETag") != `"`+record.Revision+`"` {
				t.Fatal("missing revision ETag")
			}
			if w := request("PUT", "/admin/v1/blueprints/engineering", "stale", raw, nil); w.Code != 409 {
				t.Fatal("lost update", w.Code)
			}
			// Low-level routes cannot change compiler-owned declarations or sets.
			for _, test := range []struct {
				method, path string
				body         []byte
			}{
				{"PUT", "/admin/v1/declarations", record.Compiled.Publication.Declarations[0]},
				{"DELETE", "/admin/v1/declarations/" + record.Compiled.Identifiers["profile"], nil},
				{"PUT", "/admin/v1/sets/" + record.Compiled.Publication.Name + "/declarations/other", nil},
			} {
				if w := request(test.method, test.path, "", test.body, nil); w.Code != 409 {
					t.Fatal("namespace guard", test.path, w.Code)
				}
			}
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: cms.Fingerprint(identity.Cert), Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue}, Device: storage.DeviceInfo{ProductName: "Mac16,1", OSVersion: "26.4"}}}); err != nil {
				t.Fatal(err)
			}
			w = request("PUT", "/admin/v1/enrollments/device/device/blueprints/engineering", "", nil, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if _, err := a.Engine.Manifest(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			path := configurationprofile.Path + info.Revision + "?channel=device&id=device"
			for _, cert := range []*x509.Certificate{nil, other.Cert} {
				if w := request("GET", path, "", nil, cert); w.Code != 403 {
					t.Fatal("identity bypass", w.Code, w.Body.String())
				}
			}
			w = request("GET", path, "", nil, identity.Cert)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), profileData) {
				t.Fatal("device download", w.Code, w.Body.String())
			}
			if w := request("GET", configurationprofile.Path+info.Revision+"?channel=user&id=other&parent=device", "", nil, identity.Cert); w.Code != 403 {
				t.Fatal("unknown user allowed", w.Code)
			}
			w = request("DELETE", "/admin/v1/enrollments/device/device/blueprints/engineering", "", nil, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := request("GET", path, "", nil, identity.Cert); w.Code != 404 {
				t.Fatal("unassignment retained access", w.Code)
			}
			w = request("DELETE", "/admin/v1/blueprints/engineering", record.Revision, nil, nil)
			if w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := request("GET", "/admin/v1/configuration-profiles/"+info.Revision+"/content", "", nil, nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), profileData) {
				t.Fatal("immutable profile lost", w.Code)
			}
		})
	}
}
