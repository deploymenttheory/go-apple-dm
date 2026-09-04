package pushnotify_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/pushnotify"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

const testTopic = "com.apple.mgmt.External.store-test"

var errStub = errors.New("stub: storage failure")

// countingCertStore counts the PushCert and PushCertVersion calls that reach
// the wrapped store.
type countingCertStore struct {
	storage.PushCertStore
	loads    atomic.Int64
	versions atomic.Int64
}

func (c *countingCertStore) PushCert(ctx context.Context, topic string) (*storage.PushCert, error) {
	c.loads.Add(1)
	return c.PushCertStore.PushCert(ctx, topic)
}

func (c *countingCertStore) PushCertVersion(ctx context.Context, topic string) (int64, error) {
	c.versions.Add(1)
	return c.PushCertStore.PushCertVersion(ctx, topic)
}

func (c *countingCertStore) counts() (loads, versions int64) {
	return c.loads.Load(), c.versions.Load()
}

// stubCertStore returns scripted records and errors.
type stubCertStore struct {
	version    int64
	versionErr error
	cert       *storage.PushCert
	certErr    error
	list       []storage.PushCert
	listErr    error
	loads      int
}

func (s *stubCertStore) StorePushCert(context.Context, string, []byte, []byte, time.Time) (storage.PushCert, error) {
	return storage.PushCert{}, errStub
}

func (s *stubCertStore) PushCert(context.Context, string) (*storage.PushCert, error) {
	s.loads++
	if s.certErr != nil {
		return nil, s.certErr
	}
	return s.cert, nil
}

func (s *stubCertStore) PushCerts(context.Context) ([]storage.PushCert, error) {
	return s.list, s.listErr
}

func (s *stubCertStore) PushCertVersion(context.Context, string) (int64, error) {
	return s.version, s.versionErr
}

// issuePushPEM returns a PEM pair for topic whose leaf is valid from
// notBefore for one day, plus the identity so tests can tell renewals apart.
func issuePushPEM(t *testing.T, ca *testpki.CA, topic string, notBefore time.Time) (certPEM, keyPEM []byte, id *testpki.Identity) {
	t.Helper()
	id, err := ca.IssuePush(topic, notBefore)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err = id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM, id
}

func TestStoreCertStoreCachesAndReloads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, err := testpki.NewCA("apns")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, first := issuePushPEM(t, ca, testTopic, t0.Add(-time.Hour))
	inner := inmem.New()
	if _, err := inner.StorePushCert(ctx, "", certPEM, keyPEM, t0); err != nil {
		t.Fatal(err)
	}
	counting := &countingCertStore{PushCertStore: inner}
	fake := clock.NewFake(t0)
	cs := pushnotify.NewStoreCertStore(counting, pushnotify.WithCertClock(fake), pushnotify.WithCertTTL(time.Minute))

	// Two calls inside the TTL read the store once and never ask for the version.
	got, err := cs.PushCertificate(ctx, testTopic)
	if err != nil {
		t.Fatal(err)
	}
	if got.Leaf == nil || !got.Leaf.Equal(first.Cert) {
		t.Fatal("first load returned the wrong leaf")
	}
	fake.Advance(30 * time.Second)
	if _, err := cs.PushCertificate(ctx, testTopic); err != nil {
		t.Fatal(err)
	}
	if loads, versions := counting.counts(); loads != 1 || versions != 0 {
		t.Fatalf("inside TTL: loads=%d versions=%d, want 1 and 0", loads, versions)
	}

	// Past the TTL with the same version: one version check, no reload.
	fake.Advance(31 * time.Second)
	if _, err := cs.PushCertificate(ctx, testTopic); err != nil {
		t.Fatal(err)
	}
	if loads, versions := counting.counts(); loads != 1 || versions != 1 {
		t.Fatalf("same version: loads=%d versions=%d, want 1 and 1", loads, versions)
	}
	// The refreshed entry is trusted for another TTL.
	fake.Advance(30 * time.Second)
	if _, err := cs.PushCertificate(ctx, testTopic); err != nil {
		t.Fatal(err)
	}
	if loads, versions := counting.counts(); loads != 1 || versions != 1 {
		t.Fatalf("refreshed entry: loads=%d versions=%d, want 1 and 1", loads, versions)
	}

	// A renewal bumps the version, so the next check past the TTL reloads.
	renewedPEM, renewedKey, renewed := issuePushPEM(t, ca, testTopic, t0.Add(-30*time.Minute))
	if _, err := inner.StorePushCert(ctx, testTopic, renewedPEM, renewedKey, fake.Now()); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute)
	got, err = cs.PushCertificate(ctx, testTopic)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Leaf.Equal(renewed.Cert) {
		t.Fatal("reload returned the old leaf")
	}
	if loads, versions := counting.counts(); loads != 2 || versions != 2 {
		t.Fatalf("after renewal: loads=%d versions=%d, want 2 and 2", loads, versions)
	}

	// An unknown topic is ErrNoCertificate and is never cached.
	for range 2 {
		if _, err := cs.PushCertificate(ctx, "com.apple.mgmt.External.nope"); !errors.Is(err, push.ErrNoCertificate) {
			t.Fatalf("unknown topic: %v", err)
		}
	}
	if loads, _ := counting.counts(); loads != 4 {
		t.Fatalf("unknown topic loads=%d, want 4 (not cached)", loads)
	}

	// TTL 0 checks the version on every call after the first load.
	perCall := &countingCertStore{PushCertStore: inner}
	cs0 := pushnotify.NewStoreCertStore(perCall, pushnotify.WithCertClock(fake), pushnotify.WithCertTTL(0))
	for range 3 {
		if _, err := cs0.PushCertificate(ctx, testTopic); err != nil {
			t.Fatal(err)
		}
	}
	if loads, versions := perCall.counts(); loads != 1 || versions != 2 {
		t.Fatalf("TTL 0: loads=%d versions=%d, want 1 and 2", loads, versions)
	}

	// Defaults (real clock, DefaultCertTTL) serve the certificate too.
	if _, err := pushnotify.NewStoreCertStore(inner).PushCertificate(ctx, testTopic); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCertStoreErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, err := testpki.NewCA("apns")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, _ := issuePushPEM(t, ca, testTopic, t0.Add(-time.Hour))
	fake := clock.NewFake(t0)
	good := &storage.PushCert{Topic: testTopic, CertPEM: certPEM, KeyPEM: keyPEM, Version: 1}

	// A version check that fails is wrapped; a version check that says the
	// topic vanished is ErrNoCertificate.
	stub := &stubCertStore{version: 1, cert: good}
	cs := pushnotify.NewStoreCertStore(stub, pushnotify.WithCertClock(fake), pushnotify.WithCertTTL(time.Second))
	if _, err := cs.PushCertificate(ctx, testTopic); err != nil {
		t.Fatal(err)
	}
	fake.Advance(2 * time.Second)
	stub.versionErr = errStub
	if _, err := cs.PushCertificate(ctx, testTopic); !errors.Is(err, errStub) {
		t.Fatalf("version error: %v", err)
	}
	stub.versionErr = storage.ErrNotFound
	if _, err := cs.PushCertificate(ctx, testTopic); !errors.Is(err, push.ErrNoCertificate) {
		t.Fatalf("version not found: %v", err)
	}

	// A load that fails after the version moved is wrapped and the old entry
	// is not served in its place.
	stub.versionErr = nil
	stub.version = 2
	stub.certErr = errStub
	if _, err := cs.PushCertificate(ctx, testTopic); !errors.Is(err, errStub) {
		t.Fatalf("load error: %v", err)
	}

	// Garbage PEM is a parse error and nothing is cached, so the next call
	// hits the store again.
	garbage := &stubCertStore{cert: &storage.PushCert{Topic: testTopic, CertPEM: []byte("not pem"), KeyPEM: keyPEM, Version: 1}}
	cs = pushnotify.NewStoreCertStore(garbage, pushnotify.WithCertClock(fake))
	for range 2 {
		if _, err := cs.PushCertificate(ctx, testTopic); !errors.Is(err, pushcert.ErrInvalid) {
			t.Fatalf("garbage PEM: %v", err)
		}
	}
	if garbage.loads != 2 {
		t.Fatalf("garbage PEM loads=%d, want 2 (not cached)", garbage.loads)
	}

	// A cold load that fails is wrapped too.
	cold := &stubCertStore{certErr: errStub}
	if _, err := pushnotify.NewStoreCertStore(cold).PushCertificate(ctx, testTopic); !errors.Is(err, errStub) {
		t.Fatalf("cold load error: %v", err)
	}
}

func TestExpiringCerts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, err := testpki.NewCA("apns")
	if err != nil {
		t.Fatal(err)
	}
	store := inmem.New()

	// An empty store lists nothing, as an empty slice rather than nil.
	got, err := pushnotify.ExpiringCerts(ctx, store, t0, time.Hour)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}

	// Two topics: "soon" expires 4h after t0, "later" 23h after t0.
	soonPEM, soonKey, _ := issuePushPEM(t, ca, "com.apple.mgmt.External.soon", t0.Add(-20*time.Hour))
	laterPEM, laterKey, _ := issuePushPEM(t, ca, "com.apple.mgmt.External.later", t0.Add(-time.Hour))
	for _, pair := range [][2][]byte{{soonPEM, soonKey}, {laterPEM, laterKey}} {
		if _, err := store.StorePushCert(ctx, "", pair[0], pair[1], t0); err != nil {
			t.Fatal(err)
		}
	}
	got, err = pushnotify.ExpiringCerts(ctx, store, t0, 5*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Topic != "com.apple.mgmt.External.soon" || got[0].KeyPEM != nil {
		t.Fatalf("within 5h: %+v", got)
	}
	got, err = pushnotify.ExpiringCerts(ctx, store, t0, 24*time.Hour)
	if err != nil || len(got) != 2 {
		t.Fatalf("within 24h: %+v %v", got, err)
	}
	got, err = pushnotify.ExpiringCerts(ctx, store, t0, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("within 0: %+v %v", got, err)
	}

	// A certificate that already expired is listed even with within 0, and a
	// storage error surfaces wrapped.
	stub := &stubCertStore{list: []storage.PushCert{
		{Topic: "past", NotAfter: t0.Add(-time.Minute)},
		{Topic: "now", NotAfter: t0},
		{Topic: "future", NotAfter: t0.Add(time.Minute)},
	}}
	got, err = pushnotify.ExpiringCerts(ctx, stub, t0, 0)
	if err != nil || len(got) != 2 || got[0].Topic != "past" || got[1].Topic != "now" {
		t.Fatalf("already past: %+v %v", got, err)
	}
	stub.listErr = errStub
	if _, err := pushnotify.ExpiringCerts(ctx, stub, t0, 0); !errors.Is(err, errStub) {
		t.Fatalf("storage error: %v", err)
	}
}
