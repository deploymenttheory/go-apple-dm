package apns_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/push/apns"
	"github.com/deploymenttheory/go-apple-mdm/push/pushtest"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
)

// TestPushWithStoreCertStore pushes through a StoreCertStore backed by the
// in-memory store and checks that a renewal stored under the same topic is
// picked up after the TTL and reaches the transport as a new leaf (decision
// record 0015, claim 4).
func TestPushWithStoreCertStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, err := testpki.NewCA("apns")
	if err != nil {
		t.Fatal(err)
	}
	srv := pushtest.NewServer()
	t.Cleanup(srv.Close)
	fake := clock.NewFake(time.Now())
	store := inmem.New()

	storePush := func(notBefore time.Time) *x509.Certificate {
		t.Helper()
		id, err := ca.IssuePush("com.apple.mgmt.External.test", notBefore)
		if err != nil {
			t.Fatal(err)
		}
		certPEM, keyPEM, err := id.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StorePushCert(ctx, "", certPEM, keyPEM, fake.Now()); err != nil {
			t.Fatal(err)
		}
		return id.Cert
	}
	first := storePush(fake.Now().Add(-time.Hour))

	// The transport builder sees the leaf the client is about to use.
	var leaves []*x509.Certificate
	transport := func(cert tls.Certificate) *http.Client {
		leaves = append(leaves, cert.Leaf)
		return srv.Client()
	}
	certs := push.NewStoreCertStore(store, push.WithCertClock(fake), push.WithCertTTL(time.Minute))
	c := apns.New(certs, apns.WithHost(srv.URL), apns.WithTransport(transport), apns.WithClock(fake))

	res, err := c.Push(ctx, []push.Target{target("a", []byte{1})})
	if err != nil {
		t.Fatal(err)
	}
	if r := res[target("a", nil).ID]; !r.Sent() {
		t.Fatalf("first push: %+v", r)
	}
	if len(leaves) != 1 || !leaves[0].Equal(first) {
		t.Fatalf("first push built %d transports, want 1 with the stored leaf", len(leaves))
	}

	// A renewal stored for the same topic is not seen inside the TTL.
	renewed := storePush(fake.Now().Add(-time.Minute))
	if res, _ := c.Push(ctx, []push.Target{target("a", []byte{2})}); !res[target("a", nil).ID].Sent() {
		t.Fatal("push inside TTL failed")
	}
	if len(leaves) != 1 {
		t.Fatalf("renewal picked up inside TTL: %d transports", len(leaves))
	}

	// Past the TTL the store reports a new version, the certificate is
	// reloaded, and apns rebuilds its transport with the renewed leaf.
	fake.Advance(2 * time.Minute)
	res, err = c.Push(ctx, []push.Target{target("a", []byte{3})})
	if err != nil {
		t.Fatal(err)
	}
	if r := res[target("a", nil).ID]; !r.Sent() {
		t.Fatalf("push after renewal: %+v", r)
	}
	if len(leaves) != 2 || !leaves[1].Equal(renewed) || leaves[1].Equal(first) {
		t.Fatalf("after renewal built %d transports, want 2 with the renewed leaf", len(leaves))
	}
	if reqs := srv.Requests(); len(reqs) != 3 || reqs[2].Token != "03" || reqs[2].Topic != "com.apple.mgmt.External.test" {
		t.Fatalf("requests %+v", reqs)
	}
}
