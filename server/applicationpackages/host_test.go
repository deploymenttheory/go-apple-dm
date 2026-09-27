package applicationpackages_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/deploymenttheory/go-macos-pkg/pkg/pkgsign"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/filesystem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/applicationpackages"
)

// packageFixture builds a signed installer and a trust policy for its temporary test CA.
func packageFixture(t *testing.T) ([]byte, applications.VerificationPolicy) {
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

// hostFixture supplies signed content, an enabled enrollment and a deterministic clock.
func hostFixture(t *testing.T) (applicationpackages.Config, applications.Record, []byte) {
	t.Helper()
	payload, policy := packageFixture(t)
	backing := state.NewMemory()
	now := time.Now().UTC()
	backing.Now = func() time.Time { return now }
	disk, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := disk.Close(); e != nil {
			t.Error(e)
		}
	})
	packages, err := applications.New(applications.Config{State: backing, Backends: map[string]applications.BlobStore{"local": disk}, ScratchDir: t.TempDir(), Verification: policy})
	if err != nil {
		t.Fatal(err)
	}
	record, err := packages.Create(t.Context(), applications.Metadata{PackageName: "Bench", FileName: "bench.pkg", BundleID: "com.example.bench", Version: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	record, err = packages.Upload(t.Context(), record.ID, record.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := applicationpackages.Config{
		Packages: packages, State: backing, BaseURL: "https://mdm.example/", Now: func() time.Time { return now },
		Target: func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			return support.Target{OS: support.MacOS, Version: osversion.New(26, 0, 0), Channel: support.ChannelDevice, Supervised: true, UserApproved: true}, nil
		},
		Authorize: func(context.Context, mdm.EnrollmentID) error { return nil },
	}
	return cfg, record, payload
}

// bearerToken extracts the test capability without printing it in test output.
func bearerToken(t *testing.T, plan applicationpackages.Plan) string {
	t.Helper()
	u, err := url.Parse(plan.ManifestURL)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, applicationpackages.Path), "/")
	if len(parts) != 2 {
		t.Fatal("invalid grant URL")
	}
	return parts[0]
}

// TestMDMAndDDMPlansShareVerifiedContent checks protocol selection, manifest integrity,
// scoped range reads, hashed persistence and explicit grant revocation.
func TestMDMAndDDMPlansShareVerifiedContent(t *testing.T) {
	cfg, record, payload := hostFixture(t)
	host, err := applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	for _, method := range []string{"mdm", "ddm"} {
		t.Run(method, func(t *testing.T) {
			plan, err := host.Prepare(t.Context(), id, record.ID, record.Content.Revision, method, 0)
			if err != nil {
				t.Fatal(err)
			}
			if method == "mdm" && (plan.Command == nil || len(plan.Publication.Declarations) != 0) {
				t.Fatal("wrong MDM plan")
			}
			if method == "ddm" && (plan.Command != nil || len(plan.Publication.Declarations) != 2) {
				t.Fatal("wrong DDM plan")
			}
			for _, raw := range plan.Publication.Declarations {
				if _, e := ddm.ParseDeclaration(raw, support.Target{}); e != nil {
					t.Fatal(e)
				}
			}
			token := bearerToken(t, plan)
			rows, err := cfg.State.List(t.Context(), "applications/download-grants/", "", 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if bytes.Contains(row.Value, []byte(token)) {
					t.Fatal("stored plaintext grant")
				}
			}
			manifest, grant, err := host.Manifest(t.Context(), token)
			if err != nil || grant.PackageID != record.ID {
				t.Fatal(err)
			}
			if err = applications.ValidateManifest(manifest, *record.Content); err != nil {
				t.Fatal(err)
			}
			content, _, err := host.Content(t.Context(), token)
			if err != nil || content.SHA256 != record.Content.SHA256 {
				t.Fatal(err)
			}
			body, _, err := host.Open(t.Context(), token, 3, 9)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(body)
			closeErr := body.Close()
			if err != nil || closeErr != nil || !bytes.Equal(got, payload[3:12]) {
				t.Fatal("range bytes differ", err, closeErr)
			}
			other := mdm.EnrollmentID{ID: "other-device", Channel: mdm.ChannelDevice}
			if err = host.Revoke(t.Context(), other, plan.Grant.ID); !errors.Is(err, applications.ErrNotFound) {
				t.Fatal("cross-enrollment revocation", err)
			}
			if err = host.Revoke(t.Context(), id, plan.Grant.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err = host.Manifest(t.Context(), token); !errors.Is(err, applications.ErrNotFound) {
				t.Fatal("revoked grant accepted", err)
			}
			if _, _, err = host.Open(t.Context(), token, 0, -1); !errors.Is(err, applications.ErrNotFound) {
				t.Fatal("revoked bytes accepted", err)
			}
			if err = host.Revoke(t.Context(), id, plan.Grant.ID); err != nil {
				t.Fatal("revocation not idempotent", err)
			}
		})
	}
}

// TestEligibilityExpiryAndDisabledEnrollments keeps unsupported devices and stale
// capabilities out of both native workflows.
func TestEligibilityExpiryAndDisabledEnrollments(t *testing.T) {
	cfg, record, _ := hostFixture(t)
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	for _, version := range []int{15, 26, 27} {
		candidate := cfg
		candidate.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			return support.Target{OS: support.MacOS, Version: osversion.New(version, 0, 0), Channel: support.ChannelDevice, Supervised: true, UserApproved: true}, nil
		}
		host, err := applicationpackages.New(candidate)
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"mdm", "ddm"} {
			_, err = host.Prepare(t.Context(), id, record.ID, record.Content.Revision, method, time.Minute)
			if version == 15 && method == "ddm" {
				if !errors.Is(err, applications.ErrIneligible) {
					t.Fatal("macOS 15 accepted DDM package", err)
				}
			} else if err != nil {
				t.Fatal(version, method, err)
			}
		}
	}
	host, err := applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := host.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := bearerToken(t, plan)
	expired := cfg
	expired.Now = func() time.Time { return plan.Grant.ExpiresAt }
	expiredHost, err := applicationpackages.New(expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = expiredHost.Content(t.Context(), token); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal("expired capability accepted", err)
	}
	disabled := cfg
	disabled.Authorize = func(context.Context, mdm.EnrollmentID) error { return applications.ErrNotFound }
	disabledHost, err := applicationpackages.New(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = disabledHost.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal(err)
	}
	if _, _, err = disabledHost.Content(t.Context(), token); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal("disabled device retained access", err)
	}
	if err = cfg.Packages.Delete(t.Context(), record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err = host.Content(t.Context(), token); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal("deleted package accessible", err)
	}
}

// TestHostValidationAndCorruptGrants rejects malformed configuration, identifiers,
// capability data and target lookups before serving package bytes.
func TestHostValidationAndCorruptGrants(t *testing.T) {
	cfg, record, _ := hostFixture(t)
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	for _, raw := range []string{"http://example.test", "https://example.test/path", "https://example.test?query=x", "https://example.test?", "https://example.test#fragment", "https://:"} {
		candidate := cfg
		candidate.BaseURL = raw
		if _, err := applicationpackages.New(candidate); err == nil {
			t.Fatal("invalid host origin", raw)
		}
	}
	if _, err := applicationpackages.New(applicationpackages.Config{}); err == nil {
		t.Fatal("missing dependencies")
	}
	defaultClock := cfg
	defaultClock.Now = nil
	if _, err := applicationpackages.New(defaultClock); err != nil {
		t.Fatal(err)
	}
	host, err := applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"", "other"} {
		if _, err = host.Prepare(t.Context(), id, record.ID, record.Content.Revision, method, 0); !errors.Is(err, applications.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err = host.Prepare(t.Context(), id, "missing", "revision", "mdm", 0); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = host.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 8*24*time.Hour); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err = host.Content(t.Context(), "invalid"); !errors.Is(err, applications.ErrNotFound) {
		t.Fatal(err)
	}
	if err = host.Revoke(t.Context(), id, "invalid"); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
	wrongTarget := cfg
	wrongTarget.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
		return support.Target{OS: support.IOS}, nil
	}
	candidate, err := applicationpackages.New(wrongTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidate.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0); !errors.Is(err, applications.ErrIneligible) {
		t.Fatal(err)
	}
	wrongTarget.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
		return support.Target{OS: support.MacOS}, nil
	}
	candidate, err = applicationpackages.New(wrongTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidate.Prepare(t.Context(), id, record.ID, record.Content.Revision, "ddm", 0); !errors.Is(err, applications.ErrIneligible) {
		t.Fatal("missing OS version accepted", err)
	}
	wrongTarget.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
		return support.Target{}, io.ErrUnexpectedEOF
	}
	candidate, err = applicationpackages.New(wrongTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidate.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	plan, err := host.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0)
	if err != nil {
		t.Fatal(err)
	}
	token := bearerToken(t, plan)
	key := "applications/download-grants/" + plan.Grant.ID
	for _, data := range [][]byte{[]byte("bad json"), []byte(`{"id":"wrong"}`)} {
		if err = cfg.State.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: data}) }); err != nil {
			t.Fatal(err)
		}
		if _, _, err = host.Content(t.Context(), token); err == nil {
			t.Fatal("corrupt grant accepted")
		}
		if string(data) == "bad json" {
			if err = host.Revoke(t.Context(), id, plan.Grant.ID); err == nil {
				t.Fatal("corrupt revoke succeeded")
			}
		}
	}
	// The grant representation includes no bearer URL or token field.
	data, err := json.Marshal(plan.Grant)
	if err != nil || bytes.Contains(data, []byte(token)) {
		t.Fatal("unsafe grant representation", err)
	}
}

// failingGrantStore injects durable read and write failures without changing package storage.
type failingGrantStore struct {
	state.Store
	readErr, writeErr error
}

// Get exposes a failed grant lookup.
func (s failingGrantStore) Get(ctx context.Context, key string) (state.Record, error) {
	if s.readErr != nil {
		return state.Record{}, s.readErr
	}
	return s.Store.Get(ctx, key)
}

// Update exposes transaction failures and wraps reads inside the transaction.
func (s failingGrantStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(failingGrantTx{Tx: tx, err: s.readErr}) })
}

type failingGrantTx struct {
	state.Tx
	err error
}

// Get exposes a failed transactional lookup during revocation.
func (s failingGrantTx) Get(ctx context.Context, key string) (state.Record, error) {
	if s.err != nil {
		return state.Record{}, s.err
	}
	return s.Tx.Get(ctx, key)
}

// TestGrantPersistenceFailures propagates storage failures and never treats failed
// revocation as success or makes a failed preparation's capability usable.
func TestGrantPersistenceFailures(t *testing.T) {
	cfg, record, _ := hostFixture(t)
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	host, err := applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := host.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"read", "write"} {
		candidate := cfg
		store := failingGrantStore{Store: cfg.State}
		if mode == "read" {
			store.readErr = io.ErrUnexpectedEOF
		} else {
			store.writeErr = io.ErrUnexpectedEOF
		}
		candidate.State = store
		broken, err := applicationpackages.New(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "read" {
			if _, _, err = broken.Content(t.Context(), bearerToken(t, plan)); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
		} else {
			failed, e := broken.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0)
			if !errors.Is(e, io.ErrUnexpectedEOF) {
				t.Fatal(e)
			}
			if _, _, e = host.Content(t.Context(), bearerToken(t, failed)); !errors.Is(e, applications.ErrNotFound) {
				t.Fatal("failed write produced usable grant", e)
			}
		}
		if err = broken.Revoke(t.Context(), id, plan.Grant.ID); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
	}
}

// TestDeliveryRejectsUnsupportedPreferencesAndTargets verifies that retained
// metadata cannot silently become an unsupported native installation promise.
func TestDeliveryRejectsUnsupportedPreferencesAndTargets(t *testing.T) {
	cfg, record, payload := hostFixture(t)
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	old := record.Content.Revision
	record.Metadata.FillExistingUsers = new(true)
	record, err := cfg.Packages.Update(t.Context(), record.ID, record.Revision, record.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	record, err = cfg.Packages.Upload(t.Context(), record.ID, record.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	host, err := applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Prepare(t.Context(), id, record.ID, record.Content.Revision, "mdm", 0); !errors.Is(err, applications.ErrUnsupported) {
		t.Fatal(err)
	}
	// Apple disallows InstallEnterpriseApplication on the user channel even if
	// an embedding application's target resolver supplies inconsistent context.
	cfg.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
		return support.Target{OS: support.MacOS, Version: osversion.New(26, 0, 0), Channel: support.ChannelUser}, nil
	}
	host, err = applicationpackages.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Prepare(t.Context(), id, record.ID, old, "mdm", 0); !errors.Is(err, applications.ErrIneligible) {
		t.Fatal(err)
	}
}
