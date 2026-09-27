package applications_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/deploymenttheory/go-macos-pkg/pkg/pkgsign"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/filesystem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ptr constructs explicitly present metadata values for the test fixture.
func ptr[T any](v T) *T { return &v }

// metadata returns a valid application identity used across catalogue tests.
func metadata() applications.Metadata {
	return applications.Metadata{PackageName: "Test Bench", FileName: "bench.pkg", BundleID: "com.example.bench", Version: "1.0"}
}

// manager creates isolated transactional metadata and private filesystem storage.
func manager(t *testing.T, policy applications.VerificationPolicy) (*applications.Manager, *filesystem.Store) {
	t.Helper()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	m, err := applications.New(applications.Config{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"local": store}, ScratchDir: t.TempDir(), Verification: policy})
	if err != nil {
		t.Fatal(err)
	}
	return m, store
}

// fixture builds a signed installer and a trust policy for its temporary test CA.
func fixture(t *testing.T) ([]byte, applications.VerificationPolicy) {
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
	anchors := x509.NewCertPool()
	anchors.AddCert(ca)
	return b.Bytes(), applications.VerificationPolicy{Anchors: anchors, AllowPrivateSigner: true}
}

// TestAllEditableMetadataSurvivesCRUDHistoryAndExport checks persistence, explicit false values, snapshots and measured-field separation.
func TestAllEditableMetadataSurvivesCRUDHistoryAndExport(t *testing.T) {
	ctx := t.Context()
	m, _ := manager(t, applications.VerificationPolicy{})
	parent, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	original := metadata()
	original.CategoryID = ptr("utilities")
	original.Info = ptr("Information\nsecond line")
	original.Notes = ptr("")
	original.Priority = ptr(0)
	original.OSRequirements = ptr("15.x, 26.1, 27.x")
	original.FillUserTemplate = ptr(false)
	original.FillExistingUsers = ptr(true)
	original.SWU = ptr(false)
	original.RebootRequired = ptr(true)
	original.SelfHealNotify = ptr(false)
	original.SelfHealingAction = ptr("nothing")
	original.OSInstall = ptr(false)
	original.SerialNumber = ptr("license-123")
	original.ParentPackageID = &parent.ID
	original.BasePath = ptr("/Applications")
	original.SuppressUpdates = ptr(false)
	original.IgnoreConflicts = ptr(true)
	original.SuppressFromDock = ptr(false)
	original.SuppressEula = ptr(true)
	original.SuppressRegistration = ptr(false)
	original.InstallLanguage = ptr("en_US")
	original.OSInstallerVersion = ptr("15.8")
	original.Format = ptr("flat-pkg")
	original.Digests = applications.Digests{MD5: strings.Repeat("a", 32), SHA256: strings.Repeat("b", 64), SHA3512: strings.Repeat("c", 128), SHA512: strings.Repeat("d", 128), HashType: "SHA3-512", HashValue: strings.Repeat("c", 128)}
	r, err := m.Create(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, r.ID)
	if err != nil || !reflect.DeepEqual(got.Metadata, original) {
		t.Fatalf("persisted metadata differs: %v\n%+v", err, got.Metadata)
	}
	// Returned pointer fields must not mutate the persisted record.
	*r.Metadata.FillExistingUsers = false
	got, err = m.Get(ctx, r.ID)
	if err != nil || !*got.Metadata.FillExistingUsers {
		t.Fatal("returned pointer mutated persistence", err)
	}
	original = got.Metadata
	original.Notes = ptr("updated")
	updated, err := m.Update(ctx, r.ID, r.Revision, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, r.ID, r.Revision, original); !errors.Is(err, applications.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	history, err := m.History(ctx, r.ID, paging.Page{Limit: 1})
	if err != nil || len(history.Items) != 1 || history.NextCursor == "" {
		t.Fatal("history page", err)
	}
	history, err = m.History(ctx, r.ID, paging.Page{Cursor: history.NextCursor})
	if err != nil || len(history.Items) != 1 || !reflect.DeepEqual(history.Items[0].Metadata, original) {
		t.Fatal("update snapshot", err)
	}
	var encoded bytes.Buffer
	if err = applications.ExportJSON(&encoded, []applications.Record{updated}); err != nil {
		t.Fatal(err)
	}
	var exported []map[string]any
	if err = json.Unmarshal(encoded.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported[0]["suppressUpdates"] != false || exported[0]["notes"] != "updated" || exported[0]["size"] != nil || exported[0]["indexed"] != nil {
		t.Fatal("export lost explicit values or invented measurements")
	}
	if _, exists := exported[0]["sha256"]; exists {
		t.Fatal("expected hash exposed as measured hash")
	}
	encoded.Reset()
	if err = applications.ExportCSV(&encoded, []applications.Record{updated}, []string{"packageName", "fillExistingUsers", "notes"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), "Test Bench,true,updated") {
		t.Fatal(encoded.String())
	}
}

// TestStrictMetadataAndNativeEligibility checks rejected input and native delivery constraints.
func TestStrictMetadataAndNativeEligibility(t *testing.T) {
	for _, body := range []string{`null`, `{"name":"ignored"}`, `{"packageName":"a","fileName":"a.pkg","indexed":true}`, `{"packageName":"a","fileName":"a.pkg"} {}`, `{"packageName":"a","fileName":"../a.pkg"}`, `{"packageName":"a","fileName":"a.pkg","priority":-1}`, `{"packageName":"a","fileName":"a.pkg","hashType":"SHA3-512","hashValue":"short"}`} {
		if _, err := applications.DecodeMetadata(strings.NewReader(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	m, err := applications.DecodeMetadata(strings.NewReader(`{"packageName":"a","fileName":"a.pkg","fillUserTemplate":false}`))
	if err != nil || m.FillUserTemplate == nil || *m.FillUserTemplate {
		t.Fatal("false was not preserved", err)
	}
	m = metadata()
	m.OSRequirements = ptr("15.x, 26.1, 27.x")
	for _, tc := range []struct {
		version string
		want    bool
	}{{"15.8.1", true}, {"26.1.0", true}, {"26.2", false}, {"27.0", true}, {"14.9", false}} {
		got, err := m.SupportsOS(tc.version)
		if err != nil || got != tc.want {
			t.Errorf("%s: %v %v", tc.version, got, err)
		}
	}
	if err = m.CheckNativeDelivery("26.2"); !errors.Is(err, applications.ErrIneligible) {
		t.Fatal(err)
	}
	m.SuppressEula = ptr(true)
	if err = m.CheckNativeDelivery("15.8"); !errors.Is(err, applications.ErrUnsupported) || !strings.Contains(err.Error(), "suppressEula") {
		t.Fatal(err)
	}
	m.SuppressEula = ptr(false)
	if err = m.CheckNativeDelivery("15.8"); err != nil {
		t.Fatal(err)
	}
}

// TestParentCyclesAndDeletion checks relationship integrity and retained deletion history.
func TestParentCyclesAndDeletion(t *testing.T) {
	ctx := t.Context()
	m, _ := manager(t, applications.VerificationPolicy{})
	parent, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	childMeta := metadata()
	childMeta.ParentPackageID = &parent.ID
	child, err := m.Create(ctx, childMeta)
	if err != nil {
		t.Fatal(err)
	}
	parentMeta := metadata()
	parentMeta.ParentPackageID = &child.ID
	if _, err = m.Update(ctx, parent.ID, parent.Revision, parentMeta); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal("cycle", err)
	}
	if err = m.Delete(ctx, parent.ID, parent.Revision); !errors.Is(err, applications.ErrConflict) {
		t.Fatal("orphaned child", err)
	}
	if err = m.Delete(ctx, child.ID, child.Revision); err != nil {
		t.Fatal(err)
	}
	if err = m.Delete(ctx, parent.ID, parent.Revision); err != nil {
		t.Fatal(err)
	}
	history, err := m.History(ctx, parent.ID, paging.Page{})
	if err != nil || len(history.Items) != 3 || history.Items[2].Action != "deleted" {
		t.Fatal("deletion history", err)
	}
}

// TestVerifiedUploadManifestAndImmutableRevisions checks signed upload, manifest integrity, ranges and complete cleanup.
func TestVerifiedUploadManifestAndImmutableRevisions(t *testing.T) {
	payload, policy := fixture(t)
	ctx := t.Context()
	m, store := manager(t, policy)
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := m.Upload(ctx, r.ID, r.Revision, "local", applications.Source{Kind: "https", Location: "https://example.test/bench.pkg?secret=redacted"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	c := uploaded.Content
	if c == nil || c.SHA3512 == c.SHA512 || len(c.MD5) != 32 || len(c.SHA256) != 64 || len(c.SHA3512) != 128 || c.Size != int64(len(payload)) || uploaded.CloudTransferStatus != "READY" {
		t.Fatalf("invalid measured content: %+v", c)
	}
	if c.Source.Location != "https://example.test/bench.pkg" || c.Verification.Revocation != "not-checked" {
		t.Fatal("false provenance/verification claim")
	}
	if _, err = m.Upload(ctx, r.ID, r.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), ""); !errors.Is(err, applications.ErrConflict) {
		t.Fatal("stale upload", err)
	}
	manifest, err := applications.BuildManifest(*c, "https://example.test/package/revision")
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := m.AssignManifest(ctx, r.ID, uploaded.Revision, "bench.plist", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Manifest.ContentRevision != c.Revision {
		t.Fatal("manifest unbound")
	}
	tampered := bytes.ReplaceAll(manifest, []byte(c.SHA256), []byte(strings.Repeat("0", 64)))
	if _, err = m.AssignManifest(ctx, r.ID, assigned.Revision, "wrong.plist", tampered); !errors.Is(err, applications.ErrIntegrity) {
		t.Fatal("tampered manifest", err)
	}
	meta := metadata()
	meta.Digests.SHA256 = strings.Repeat("0", 64)
	if _, err = m.Update(ctx, r.ID, assigned.Revision, meta); !errors.Is(err, applications.ErrIntegrity) {
		t.Fatal("contradictory metadata", err)
	}
	meta = metadata()
	meta.Version = "2.0"
	changed, err := m.Update(ctx, r.ID, assigned.Revision, meta)
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.Revision(ctx, r.ID, c.Revision)
	if err != nil || old.Metadata.Version != "1.0" {
		t.Fatal("immutable metadata changed", err)
	}
	updated, err := m.Upload(ctx, r.ID, changed.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), strings.ToUpper(c.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Manifest != nil || updated.Content.Revision == c.Revision {
		t.Fatal("stale manifest or reused revision")
	}
	body, _, err := m.Open(ctx, r.ID, c.Revision, 3, 8)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	closeErr := body.Close()
	if err != nil || closeErr != nil || !bytes.Equal(data, payload[3:11]) {
		t.Fatal("range mismatch", err, closeErr)
	}
	if err = m.Delete(ctx, r.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(ctx, c.Key, 0, -1); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal("orphaned blob", err)
	}
}

// TestRejectUnsignedTamperedUntrustedAndWrongDigest checks that rejected uploads cannot publish a content revision.
func TestRejectUnsignedTamperedUntrustedAndWrongDigest(t *testing.T) {
	payload, policy := fixture(t)
	for _, tc := range []struct {
		name   string
		data   []byte
		policy applications.VerificationPolicy
		hash   string
	}{
		{"untrusted", payload, applications.VerificationPolicy{}, ""},
		{"corrupt", append([]byte("bad!"), payload[4:]...), policy, ""},
		{"wrong digest", payload, policy, strings.Repeat("0", 64)},
		{"require timestamp", payload, applications.VerificationPolicy{Anchors: policy.Anchors, AllowPrivateSigner: true, RequireTimestamp: true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := manager(t, tc.policy)
			r, err := m.Create(t.Context(), metadata())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Upload(t.Context(), r.ID, r.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(tc.data), tc.hash); !errors.Is(err, applications.ErrIntegrity) {
				t.Fatal(err)
			}
			current, err := m.Get(t.Context(), r.ID)
			if err != nil || current.Content != nil || current.Revision != r.Revision {
				t.Fatal("failed upload published", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := applications.VerifyPackage(ctx, bytes.NewReader(payload), int64(len(payload)), policy); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// corruptStore simulates changed cloud bytes after a successful upload.
type corruptStore struct {
	applications.BlobStore
	key      string
	deleted  bool
	afterPut func()
}

// Put records the object and optionally races a metadata change after storage succeeds.
func (s *corruptStore) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	if err := s.BlobStore.Put(ctx, key, r, size); err != nil {
		return err
	}
	s.key = key
	if s.afterPut != nil {
		s.afterPut()
	}
	return nil
}

// Open returns mismatching readback data unless the test is exercising a CAS conflict.
func (s *corruptStore) Open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if s.afterPut != nil {
		return s.BlobStore.Open(ctx, key, offset, length)
	}
	return io.NopCloser(strings.NewReader("corrupted storage bytes")), nil
}

// Delete records cleanup and removes the incomplete object.
func (s *corruptStore) Delete(ctx context.Context, key string) error {
	s.deleted = true
	return s.BlobStore.Delete(ctx, key)
}

// TestReadbackAndConcurrentMetadataFailure checks that both corruption and stale
// publication remove the new object without changing committed metadata.
func TestReadbackAndConcurrentMetadataFailure(t *testing.T) {
	payload, policy := fixture(t)
	for _, race := range []bool{false, true} {
		t.Run(fmt.Sprint(race), func(t *testing.T) {
			_, disk := manager(t, policy)
			backend := &corruptStore{BlobStore: disk}
			m, err := applications.New(applications.Config{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"test": backend}, ScratchDir: t.TempDir(), Verification: policy})
			if err != nil {
				t.Fatal(err)
			}
			r, err := m.Create(t.Context(), metadata())
			if err != nil {
				t.Fatal(err)
			}
			expectedErr := applications.ErrIntegrity
			if race {
				expectedErr = applications.ErrConflict
				backend.afterPut = func() {
					meta := metadata()
					meta.Notes = ptr("concurrent update")
					if _, e := m.Update(t.Context(), r.ID, r.Revision, meta); e != nil {
						t.Error(e)
					}
				}
			}
			if _, err = m.Upload(t.Context(), r.ID, r.Revision, "test", applications.Source{Kind: "upload"}, bytes.NewReader(payload), ""); !errors.Is(err, expectedErr) {
				t.Fatal(err)
			}
			if !backend.deleted {
				t.Fatal("failed upload left an object")
			}
			current, err := m.Get(t.Context(), r.ID)
			if err != nil || current.Content != nil {
				t.Fatal("failed upload published", err)
			}
			if race && (current.Metadata.Notes == nil || *current.Metadata.Notes != "concurrent update") {
				t.Fatal("concurrent metadata overwritten")
			}
		})
	}
}

// TestHTTPSImport tests TLS streaming, allowlists, redirects and signed URL redaction.
func TestHTTPSImport(t *testing.T) {
	payload, policy := fixture(t)
	m, _ := manager(t, policy)
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/package":
			_, _ = w.Write(payload)
		case "/redirect":
			http.Redirect(w, r, "http://untrusted.test/steal", http.StatusFound)
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer endpoint.Close()
	u, err := url.Parse(endpoint.URL)
	if err != nil {
		t.Fatal(err)
	}
	source := applications.HTTPSSource{Client: endpoint.Client(), AllowedHosts: []string{u.Host}}
	r, err := m.Create(t.Context(), metadata())
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := source.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/package?secret=token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uploaded.Content.Source.Location, "token") {
		t.Fatal("signed query persisted")
	}
	for _, location := range []string{endpoint.URL + "/redirect?secret=token", endpoint.URL + "/missing?secret=token", "https://untrusted.test/package?secret=token"} {
		_, err = source.Import(t.Context(), m, r.ID, uploaded.Revision, "local", location)
		if err == nil || strings.Contains(err.Error(), "token") {
			t.Fatalf("unsafe source error: %v", err)
		}
	}
}

// TestJamfSchemaFieldCoverage guards the independently captured vendor field set,
// including fields whose operational values are computed rather than editable.
func TestJamfSchemaFieldCoverage(t *testing.T) {
	data, err := os.ReadFile("testdata/jamf-package-fields.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source    string   `json:"source"`
		Reference string   `json:"reference"`
		Fields    []string `json:"fields"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, f := range applications.PackageFields() {
		available[f] = true
	}
	if len(fixture.Fields) != 36 {
		t.Fatalf("unexpected reference schema size: %d", len(fixture.Fields))
	}
	for _, field := range fixture.Fields {
		if !available[field] {
			t.Errorf("missing package field %s", field)
		}
	}
}

// TestBulkDeletionReportsPartialFailures checks validation before mutation and
// preserves successes when a later package has a stale revision.
func TestBulkDeletionReportsPartialFailures(t *testing.T) {
	m, _ := manager(t, applications.VerificationPolicy{})
	ctx := t.Context()
	first, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	requests := []applications.DeleteRequest{{ID: first.ID, Revision: first.Revision}, {ID: second.ID, Revision: "stale"}}
	got, err := m.DeleteMany(ctx, requests)
	if err != nil || len(got) != 2 || got[0].Err != nil || !errors.Is(got[1].Err, applications.ErrConflict) {
		t.Fatal(got, err)
	}
	if _, err = m.Get(ctx, second.ID); err != nil {
		t.Fatal("failed deletion lost the record", err)
	}
}

// TestHTTPSRedirectPolicy enforces origin checks after every redirect, bounds
// redirect loops, honors stricter caller policy and rejects known oversized sources.
func TestHTTPSRedirectPolicy(t *testing.T) {
	payload, policy := fixture(t)
	m, _ := manager(t, policy)
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/same":
			http.Redirect(w, r, "/package", http.StatusFound)
		case "/large":
			w.Header().Set("Content-Length", "3000000000")
			w.WriteHeader(http.StatusOK)
		default:
			_, _ = w.Write(payload)
		}
	}))
	defer endpoint.Close()
	u, err := url.Parse(endpoint.URL)
	if err != nil {
		t.Fatal(err)
	}
	source := applications.HTTPSSource{Client: endpoint.Client(), AllowedHosts: []string{u.Host}}
	r, err := m.Create(t.Context(), metadata())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatal(err)
	}
	if _, err = source.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/large"); !errors.Is(err, applications.ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err = source.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/same"); err != nil {
		t.Fatal(err)
	}
	source.Client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if _, err = source.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/same"); err == nil {
		t.Fatal("caller redirect policy ignored")
	}
	for _, invalid := range []applications.HTTPSSource{{}, {Client: endpoint.Client()}, {Client: endpoint.Client(), AllowedHosts: []string{"bad/host"}}} {
		if _, err = invalid.Import(t.Context(), m, r.ID, r.Revision, "local", endpoint.URL+"/package"); !errors.Is(err, applications.ErrInvalid) {
			t.Fatal(err)
		}
	}
}
