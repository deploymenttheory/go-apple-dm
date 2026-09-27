package app_test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/deploymenttheory/go-macos-pkg/pkg/pkgsign"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	admininmem "github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/applicationpackages"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// apiPackageFixture builds a signed installer and a trust policy for its temporary test CA.
func apiPackageFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Package test root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test installer", OrganizationalUnit: []string{"TESTTEAM"}}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}
	der, err = x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := pkgsign.NewSigner(&pkgsign.Identity{Cert: leaf, Key: key, Chain: []*x509.Certificate{ca}}, pkgsign.SignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "payload.txt"), []byte("installer fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err = flatpkg.BuildComponent(flatpkg.ComponentOptions{Root: root, Identifier: "com.example.bench", Version: "1.0", Signer: signer, TempDir: t.TempDir()}, &b); err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "signer-ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), caFile
}

// TestApplicationPackageAPI exercises verified package lifecycle and delivery
// through authenticated routes on both memory and transactional SQLite storage.
func TestApplicationPackageAPI(t *testing.T) {
	payload, caFile := apiPackageFixture(t)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer source.Close()
	httpsCA := filepath.Join(t.TempDir(), "https-ca.pem")
	if err := os.WriteFile(httpsCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceURL, err := url.Parse(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, storageKind := range []string{"inmem", "sqlite"} {
		t.Run(storageKind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "source.pkg"), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			a := build(t, app.Config{Storage: storageKind, DSN: filepath.Join(root, "db.sqlite"), BootstrapToken: "admin", ApplicationPackages: app.ApplicationPackageConfig{
				PublicURL: "https://mdm.example", ScratchDir: filepath.Join(root, "scratch"), ImportDir: root, SignerCAFile: caFile, AllowPrivateSigner: true,
				HTTPSCAFile: httpsCA, AllowedHTTPSHosts: []string{sourceURL.Host}, Backends: map[string]app.ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: filepath.Join(root, "packages")}},
			}})
			call := func(method, path string, body io.Reader, revision string, status int) *httptest.ResponseRecorder {
				t.Helper()
				headers := map[string]string{}
				if revision != "" {
					headers["If-Match"] = `"` + revision + `"`
				}
				w := identityRequest(t, a, method, path, body, headers)
				if w.Code != status {
					t.Fatalf("%s %s: %d, %s", method, path, w.Code, w.Body.String())
				}
				return w
			}
			decode := func(w *httptest.ResponseRecorder) applications.Record {
				t.Helper()
				var result applications.Record
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Header().Get("ETag") != `"`+result.Revision+`"` {
					t.Fatal("missing revision")
				}
				return result
			}
			metadata := `{"packageName":"Bench","fileName":"bench.pkg","bundleId":"com.example.bench","version":"1.0","priority":0,"fillExistingUsers":false,"notes":"reviewed"}`
			base := "/application-packages"
			record := decode(call("POST", base, strings.NewReader(metadata), "", 201))
			path := base + "/" + record.ID
			call("GET", path, nil, "", 200)
			call("PUT", path, strings.NewReader(metadata), "stale", 409)
			record = decode(call("PUT", path, strings.NewReader(metadata), record.Revision, 200))
			record = decode(call("POST", path+"/upload?backend=local", bytes.NewReader(payload), record.Revision, 200))
			if record.Content == nil || record.Content.Size != int64(len(payload)) {
				t.Fatal("missing verified content")
			}
			immutable := record.Content.Revision
			downloaded := call("GET", path+"/revisions/"+immutable+"/content", nil, "", 200)
			if !bytes.Equal(downloaded.Body.Bytes(), payload) {
				t.Fatal("download mismatch")
			}
			manifest, err := applications.BuildManifest(*record.Content, "https://example.test/bench.pkg")
			if err != nil {
				t.Fatal(err)
			}
			record = decode(call("POST", path+"/manifest?filename=bench.plist", bytes.NewReader(manifest), record.Revision, 200))
			if record.Manifest == nil {
				t.Fatal("manifest not assigned")
			}
			record = decode(call("DELETE", path+"/manifest", nil, record.Revision, 200))
			record = decode(call("POST", path+"/history", strings.NewReader(`{"note":"approved"}`), record.Revision, 200))
			for _, suffix := range []string{"?limit=1", "/export?format=json", "/export?format=csv&fields=id,packageName", "/" + record.ID + "/history?limit=1", "/" + record.ID + "/history/export?format=json", "/" + record.ID + "/history/export?format=csv"} {
				call("GET", base+suffix, nil, "", 200)
			}
			for _, body := range []string{`{"kind":"file","location":"source.pkg","backend":"local"}`, `{"kind":"https","location":"` + source.URL + `/package.pkg","backend":"local"}`} {
				record = decode(call("POST", path+"/source", strings.NewReader(body), record.Revision, 200))
			}
			for _, version := range []string{"15.8", "26.0"} {
				id := mdm.EnrollmentID{ID: "device-" + version, Channel: mdm.ChannelDevice}
				err := a.Core.ImportEnrollment(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{ProductName: "VirtualMac2,1", OSVersion: version}, Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue, UserApproved: storage.CapabilityTrue}}})
				if err != nil {
					t.Fatal(err)
				}
				for _, method := range []string{"mdm", "ddm"} {
					status := 202
					if version == "15.8" && method == "ddm" {
						status = 400
					}
					deliveryPath := "/enrollments/device/" + id.ID + "/application-packages"
					w := call("POST", deliveryPath, strings.NewReader(`{"packageId":"`+record.ID+`","contentRevision":"`+immutable+`","method":"`+method+`"}`), "", status)
					if status == 400 {
						continue
					}
					var result struct {
						Grant       applicationpackages.Grant `json:"Grant"`
						Status      string                    `json:"Status"`
						CommandUUID string                    `json:"CommandUUID"`
						Set         string                    `json:"Set"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Status != "queued" || result.Grant.PackageID != record.ID {
						t.Fatal("incorrect delivery result")
					}
					manifestURL := ""
					if method == "mdm" {
						queued, e := a.Store.Commands(t.Context(), id, storage.CommandQuery{RequestType: "InstallEnterpriseApplication"}, paging.Page{Limit: 10})
						if e != nil {
							t.Fatal(e)
						}
						for _, q := range queued.Items {
							if q.Command.UUID == result.CommandUUID {
								var envelope struct {
									Command struct {
										ManifestURL string `plist:"ManifestURL"`
									} `plist:"Command"`
								}
								if e = plist.Unmarshal(q.Command.Raw, &envelope); e != nil {
									t.Fatal(e)
								}
								manifestURL = envelope.Command.ManifestURL
							}
						}
					} else {
						declaration, e := a.Engine.GetDeclaration(t.Context(), result.Set)
						if e != nil {
							t.Fatal(e)
						}
						var envelope struct {
							Payload struct {
								ManifestURL string `json:"ManifestURL"`
							} `json:"Payload"`
						}
						if e = json.Unmarshal(declaration.Canonical, &envelope); e != nil {
							t.Fatal(e)
						}
						manifestURL = envelope.Payload.ManifestURL
					}
					if manifestURL == "" {
						t.Fatal("delivery has no manifest")
					}
					packageURL := strings.TrimSuffix(manifestURL, "manifest.plist") + "package.pkg"
					for _, tc := range []struct {
						method, url string
						status      int
					}{
						{"GET", manifestURL, 200},
						{"HEAD", manifestURL, 200},
						{"GET", packageURL, 200},
						{"HEAD", packageURL, 200},
						{"GET", strings.Replace(manifestURL, "https:", "http:", 1), 404},
						{"GET", strings.Replace(packageURL, "https:", "http:", 1), 404},
					} {
						request := httptest.NewRequestWithContext(t.Context(), tc.method, tc.url, nil)
						response := httptest.NewRecorder()
						a.Handler.ServeHTTP(response, request)
						if response.Code != tc.status {
							t.Fatal("grant download failed", response.Code)
						}
						if tc.status == 200 && tc.method == "GET" && tc.url == packageURL && !bytes.Equal(response.Body.Bytes(), payload) {
							t.Fatal("grant served wrong revision")
						}
						if tc.method == "HEAD" && response.Body.Len() != 0 {
							t.Fatal("HEAD wrote content")
						}
					}
					call("DELETE", deliveryPath+"/grants/"+result.Grant.ID, nil, "", 204)
					for _, address := range []string{manifestURL, packageURL} {
						response := httptest.NewRecorder()
						a.Handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", address, nil))
						if response.Code != 404 {
							t.Fatal("revoked download accepted", response.Code)
						}
					}
				}
			}
			call("DELETE", path, nil, record.Revision, 204)
			call("GET", path, nil, "", 404)
			call("GET", path+"/history", nil, "", 200)
			second := decode(call("POST", base, strings.NewReader(metadata), "", 201))
			call("POST", base+"/delete-multiple", strings.NewReader(`{"packages":[{"id":"`+second.ID+`","revision":"`+second.Revision+`"}]}`), "", 200)
			call("GET", base+"/"+second.ID, nil, "", 404)
		})
	}
}

// TestApplicationPackageChunkedHTTP uses real HTTP/1.1 connections with no
// Content-Length and checks complete, oversized and prematurely ended uploads.
func TestApplicationPackageChunkedHTTP(t *testing.T) {
	payload, caFile := apiPackageFixture(t)
	for _, mode := range []string{"complete", "oversized", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			maxBytes := int64(len(payload))
			if mode == "oversized" {
				maxBytes--
			}
			a := build(t, app.Config{Storage: "inmem", BootstrapToken: "admin", ApplicationPackages: app.ApplicationPackageConfig{
				PublicURL: "https://mdm.example", ScratchDir: filepath.Join(root, "scratch"), SignerCAFile: caFile, AllowPrivateSigner: true, MaxBytes: maxBytes,
				Backends: map[string]app.ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: filepath.Join(root, "packages")}},
			}})
			record, err := a.ApplicationPackages.Create(t.Context(), applications.Metadata{PackageName: "Bench", FileName: "bench.pkg"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var observedChunked bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				observedChunked = r.ProtoMajor == 1 && r.ContentLength == -1 && len(r.TransferEncoding) == 1 && r.TransferEncoding[0] == "chunked"
				a.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			path := "/admin/v1/application-packages/" + record.ID + "/upload?backend=local"
			if mode == "interrupted" {
				u, err := url.Parse(srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", u.Host)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				if err = conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
					t.Fatal(err)
				}
				// Announce a chunk larger than the bytes sent, then end the write side.
				headers := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer admin\r\nIf-Match: %q\r\nTransfer-Encoding: chunked\r\nContent-Type: application/octet-stream\r\n\r\n100\r\nshort", path, u.Host, record.Revision)
				if _, err = io.WriteString(conn, headers); err != nil {
					t.Fatal(err)
				}
				tcp, ok := conn.(*net.TCPConn)
				if !ok {
					t.Fatal("expected TCP connection")
				}
				if err = tcp.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if readErr != nil || closeErr != nil {
					t.Fatal(readErr, closeErr)
				}
				if response.StatusCode != 400 {
					t.Fatal("interrupted upload status", response.StatusCode)
				}
			} else {
				input, writer := io.Pipe()
				defer func() { _ = input.Close() }()
				writeResult := make(chan error, 1)
				go func() {
					var err error
					for offset := 0; offset < len(payload); offset += 37 {
						if _, err = writer.Write(payload[offset:min(offset+37, len(payload))]); err != nil {
							break
						}
					}
					closeErr := writer.Close()
					if err == nil {
						err = closeErr
					}
					writeResult <- err
				}()
				request, err := http.NewRequestWithContext(t.Context(), "POST", srv.URL+path, input)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer admin")
				request.Header.Set("If-Match", `"`+record.Revision+`"`)
				request.Header.Set("Content-Type", "application/octet-stream")
				client := srv.Client()
				client.Timeout = 10 * time.Second
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if readErr != nil || closeErr != nil {
					t.Fatal(readErr, closeErr)
				}
				if err = <-writeResult; err != nil && mode == "complete" {
					t.Fatal(err)
				}
				want := 200
				if mode == "oversized" {
					want = 413
				}
				if response.StatusCode != want {
					t.Fatalf("status %d: %s", response.StatusCode, data)
				}
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("upload handler did not finish")
			}
			if !observedChunked {
				t.Fatal("test did not exercise chunked HTTP")
			}
			current, err := a.ApplicationPackages.Get(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "complete" {
				if current.Content == nil || current.Content.Size != int64(len(payload)) {
					t.Fatal("missing verified revision")
				}
				body, _, err := a.ApplicationPackages.Open(t.Context(), record.ID, current.Content.Revision, 0, -1)
				if err != nil {
					t.Fatal(err)
				}
				actual, readErr := io.ReadAll(body)
				closeErr := body.Close()
				if readErr != nil || closeErr != nil || !bytes.Equal(actual, payload) {
					t.Fatal("chunked content differs", readErr, closeErr)
				}
			} else if current.Content != nil || current.Revision != record.Revision {
				t.Fatal("failed upload changed catalogue")
			}
			staged, err := os.ReadDir(filepath.Join(root, "scratch"))
			if err != nil {
				t.Fatal(err)
			}
			if len(staged) != 0 {
				t.Fatal("staging file leaked")
			}
		})
	}
}

// TestApplicationPackageAPIRejectsInvalidMutations keeps malformed and stale
// requests from changing catalogue state, including bulk and manifest operations.
func TestApplicationPackageAPIRejectsInvalidMutations(t *testing.T) {
	root := t.TempDir()
	a := build(t, app.Config{Storage: "inmem", BootstrapToken: "admin", ApplicationPackages: app.ApplicationPackageConfig{
		PublicURL: "https://mdm.example", ScratchDir: filepath.Join(root, "scratch"), Backends: map[string]app.ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: filepath.Join(root, "blobs")}},
	}})
	record, err := a.ApplicationPackages.Create(t.Context(), applications.Metadata{PackageName: "Fixture", FileName: "fixture.pkg"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/application-packages/" + record.ID
	id := seed(t, a, "fixture-device")
	delivery := "/enrollments/device/" + id.ID + "/application-packages"
	for _, tc := range []struct {
		method, path, body, revision string
		status                       int
	}{
		{"POST", "/application-packages", `{}`, "", 400},
		{"POST", "/application-packages", `{"packageName":"Bad","fileName":"../bad.pkg"}`, "", 400},
		{"GET", "/application-packages?limit=invalid", "", "", 400},
		{"GET", "/application-packages?cursor=bad%2Fcursor", "", "", 400},
		{"GET", "/application-packages/export?limit=invalid", "", "", 400},
		{"GET", "/application-packages/export?cursor=bad%2Fcursor", "", "", 400},
		{"GET", "/application-packages/export?format=csv&fields=unknown", "", "", 400},
		{"POST", "/application-packages/delete-multiple", `{"packages":[]}`, "", 400},
		{"POST", "/application-packages/delete-multiple", `{"unknown":true}`, "", 400},
		{"PUT", path, `{`, record.Revision, 400},
		{"DELETE", path, "", "stale", 409},
		{"POST", path + "/upload?backend=missing", "bytes", record.Revision, 400},
		{"POST", path + "/manifest?filename=fixture.plist", "invalid", record.Revision, 400},
		{"DELETE", path + "/manifest", "", "stale", 409},
		{"GET", path + "/history?limit=invalid", "", "", 400},
		{"GET", path + "/history?cursor=invalid", "", "", 400},
		{"GET", path + "/history/export?limit=invalid", "", "", 400},
		{"GET", path + "/history/export?cursor=invalid", "", "", 400},
		{"POST", path + "/history", `{"note":""}`, record.Revision, 400},
		{"POST", path + "/history", `{`, record.Revision, 400},
		{"POST", path + "/history", `{"note":"x"} {}`, record.Revision, 400},
		{"POST", path + "/history", `{"note":"x"}` + strings.Repeat(" ", app.MaxAdminBody), record.Revision, 413},
		{"POST", path + "/source", `{`, record.Revision, 400},
		{"POST", path + "/source", `{"kind":"file","location":"fixture.pkg","backend":"local"}`, record.Revision, 400},
		{"POST", path + "/source", `{"kind":"https","location":"https://unapproved.test/pkg","backend":"local"}`, record.Revision, 400},
		{"POST", path + "/source", `{"kind":"unknown"}`, record.Revision, 400},
		{"GET", path + "/revisions/missing/content", "", "", 404},
		{"POST", delivery, `{`, "", 400},
		{"POST", delivery, `{"ttlSeconds":-1}`, "", 400},
		{"POST", delivery, `{"ttlSeconds":604801}`, "", 400},
		{"POST", delivery, `{"packageId":"missing","contentRevision":"missing","method":"mdm"}`, "", 404},
		{"DELETE", delivery + "/grants/invalid", "", "", 400},
	} {
		t.Run(tc.method+tc.path+tc.body[:min(len(tc.body), 24)], func(t *testing.T) {
			w := identityRequest(t, a, tc.method, tc.path, strings.NewReader(tc.body), map[string]string{"If-Match": `"` + tc.revision + `"`})
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	after, err := a.ApplicationPackages.Get(t.Context(), record.ID)
	if err != nil || after.Revision != record.Revision || after.Content != nil {
		t.Fatal("failed request mutated catalogue", err)
	}
}

// TestApplicationPackagePermissions separates upload, read, download and delivery
// authority even when the caller knows the package and enrollment identifiers.
func TestApplicationPackagePermissions(t *testing.T) {
	root := t.TempDir()
	store := admininmem.New()
	a := build(t, app.Config{Storage: "inmem", AdminStore: store, ApplicationPackages: app.ApplicationPackageConfig{
		PublicURL: "https://mdm.example", ScratchDir: filepath.Join(root, "scratch"), Backends: map[string]app.ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: filepath.Join(root, "blobs")}},
	}})
	registry, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := adminauth.New(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	token := mintPrincipal(t, manager, adminauth.Principal{Name: "uploader", Roles: []string{"uploader"}})
	if _, err = manager.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "upload-only", Source: `permit(principal in MDM::Role::"uploader",action == MDM::Action::"uploadApplicationPackage",resource);`}); err != nil {
		t.Fatal(err)
	}
	record, err := a.ApplicationPackages.Create(t.Context(), applications.Metadata{PackageName: "Fixture", FileName: "fixture.pkg"})
	if err != nil {
		t.Fatal(err)
	}
	id := seed(t, a, "fixture-device")
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/application-packages/" + record.ID + "/upload?backend=local", 400},
		{"GET", "/application-packages/" + record.ID, 403},
		{"POST", "/application-packages", 403},
		{"GET", "/application-packages/" + record.ID + "/revisions/content/content", 403},
		{"POST", "/enrollments/device/" + id.ID + "/application-packages", 403},
	} {
		for _, auth := range []string{"Bearer " + token, ""} {
			w := identityRequest(t, a, tc.method, tc.path, strings.NewReader("invalid"), map[string]string{"Authorization": auth, "If-Match": `"` + record.Revision + `"`})
			want := tc.status
			if auth == "" {
				want = 401
			}
			if w.Code != want {
				t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
			}
		}
	}
}

// TestApplicationPackageDeliveryRollback injects failures after grant creation
// and proves SQL delivery never leaves an unrecorded command, set or capability.
func TestApplicationPackageDeliveryRollback(t *testing.T) {
	payload, caFile := apiPackageFixture(t)
	for _, tc := range []struct {
		name, method, trigger string
		status                int
	}{
		{"command insert", "mdm", "CREATE TRIGGER reject_package BEFORE INSERT ON commands WHEN 1 BEGIN SELECT RAISE(ABORT, 'private injected failure'); END", 500},
		{"declaration insert", "ddm", "CREATE TRIGGER reject_package BEFORE INSERT ON ddm_declarations WHEN 1 BEGIN SELECT RAISE(ABORT, 'private injected failure'); END", 500},
		{"assignment insert", "ddm", "CREATE TRIGGER reject_package BEFORE INSERT ON ddm_enrollment_sets WHEN 1 BEGIN SELECT RAISE(ABORT, 'private injected failure'); END", 500},
		{"MDM audit capture", "mdm", "CREATE TRIGGER reject_package BEFORE INSERT ON event_records WHEN NEW.type = 'admin-action' BEGIN SELECT RAISE(ABORT, 'private injected failure'); END", 503},
		{"DDM audit capture", "ddm", "CREATE TRIGGER reject_package BEFORE INSERT ON event_records WHEN NEW.type = 'admin-action' BEGIN SELECT RAISE(ABORT, 'private injected failure'); END", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dsn := filepath.Join(root, "db.sqlite")
			a := build(t, app.Config{Storage: "sqlite", DSN: dsn, BootstrapToken: "admin", Sinks: app.SinkConfig{Persist: true}, ApplicationPackages: app.ApplicationPackageConfig{
				PublicURL: "https://mdm.example", ScratchDir: filepath.Join(root, "scratch"), SignerCAFile: caFile, AllowPrivateSigner: true,
				Backends: map[string]app.ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: filepath.Join(root, "blobs")}},
			}})
			record, err := a.ApplicationPackages.Create(t.Context(), applications.Metadata{PackageName: "Bench", FileName: "bench.pkg", BundleID: "com.example.bench", Version: "1.0"})
			if err != nil {
				t.Fatal(err)
			}
			record, err = a.ApplicationPackages.Upload(t.Context(), record.ID, record.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
			if err != nil {
				t.Fatal(err)
			}
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			if err = a.Core.ImportEnrollment(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{ProductName: "VirtualMac2,1", OSVersion: "26.0"}, Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue, UserApproved: storage.CapabilityTrue}}}); err != nil {
				t.Fatal(err)
			}
			sqlStore, err := sqlite.Open(t.Context(), dsn, sqlite.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlStore.Close() }()
			db := sqlStore.DB()
			if _, err = db.ExecContext(t.Context(), tc.trigger); err != nil {
				t.Fatal(err)
			}
			w := identityRequest(t, a, "POST", "/enrollments/device/device/application-packages", strings.NewReader(`{"packageId":"`+record.ID+`","contentRevision":"`+record.Content.Revision+`","method":"`+tc.method+`"}`), nil)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private injected") {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, query := range []string{
				"SELECT COUNT(*) FROM protocol_state WHERE record_key LIKE 'applications/download-grants/%'",
				"SELECT COUNT(*) FROM commands", "SELECT COUNT(*) FROM ddm_sets", "SELECT COUNT(*) FROM ddm_declarations", "SELECT COUNT(*) FROM ddm_enrollment_sets",
			} {
				var count int
				if err = db.QueryRowContext(t.Context(), query).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed delivery retained state", count, err)
				}
			}
			after, err := a.ApplicationPackages.Get(t.Context(), record.ID)
			if err != nil || after.Revision != record.Revision {
				t.Fatal("delivery failure changed catalogue", err)
			}
		})
	}
}
