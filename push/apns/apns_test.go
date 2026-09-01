package apns_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/push/apns"
	"github.com/deploymenttheory/go-apple-mdm/push/pushtest"
)

func pushCert(t *testing.T, ca *testpki.CA, notBefore time.Time) tls.Certificate {
	t.Helper()
	id, err := ca.Issue("com.apple.mgmt.External.test", notBefore)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{id.Cert.Raw}, PrivateKey: id.Key, Leaf: id.Cert}
}

func target(id string, token []byte) push.Target {
	return push.Target{ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: id}, Push: mdm.Push{Topic: "com.apple.mgmt.External.test", Token: token, Magic: "magic-" + id}}
}

func newClient(t *testing.T, srv *pushtest.Server, store push.CertStore, opts ...apns.Option) *apns.Client {
	t.Helper()
	// The test server is TLS with its own CA; route the client's per-topic
	// transport through the server's client so the certificate is trusted.
	transport := func(tls.Certificate) *http.Client { return srv.Client() }
	return apns.New(store, append([]apns.Option{apns.WithHost(srv.URL), apns.WithTransport(transport)}, opts...)...)
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, _ := testpki.NewCA("apns")
	srv := pushtest.NewServer()
	t.Cleanup(srv.Close)
	store := push.StaticCertStore{"com.apple.mgmt.External.test": pushCert(t, ca, time.Now().Add(-time.Hour))}
	c := newClient(t, srv, store)
	ok, gone, bad, busy, boom := []byte{1}, []byte{2}, []byte{3}, []byte{4}, []byte{5}
	srv.ScriptToken(gone, pushtest.Script{Status: 410, Reason: "Unregistered"})
	srv.ScriptToken(bad, pushtest.Script{Status: 400, Reason: "BadDeviceToken"})
	srv.ScriptToken(busy, pushtest.Script{Status: 429, Reason: "TooManyRequests", RetryAfter: 7})
	srv.ScriptToken(boom, pushtest.Script{Status: 500, Reason: "InternalServerError"})
	targets := []push.Target{target("ok", ok), target("gone", gone), target("bad", bad), target("busy", busy), target("boom", boom), {ID: mdm.EnrollmentID{ID: "empty"}}}
	res, err := c.Push(ctx, targets)
	if err != nil {
		t.Fatal(err)
	}
	if r := res[targets[0].ID]; !r.Sent || r.Status != 200 || r.APNSID == "" || r.Err != nil {
		t.Errorf("ok: %+v", r)
	}
	if r := res[targets[1].ID]; !r.Invalid || !errors.Is(r.Err, push.ErrInvalidToken) || r.Reason != "Unregistered" {
		t.Errorf("gone: %+v", r)
	}
	if r := res[targets[2].ID]; !r.Invalid || r.Status != 400 {
		t.Errorf("bad: %+v", r)
	}
	if r := res[targets[3].ID]; r.Invalid || !errors.Is(r.Err, push.ErrRateLimited) || r.RetryAfter != 7*time.Second {
		t.Errorf("busy: %+v", r)
	}
	if r := res[targets[4].ID]; r.Invalid || !errors.Is(r.Err, push.ErrUpstream) || r.Status != 500 {
		t.Errorf("boom: %+v", r)
	}
	if r := res[targets[5].ID]; !r.Invalid || !errors.Is(r.Err, push.ErrInvalidToken) {
		t.Errorf("empty push info: %+v", r)
	}
	reqs := srv.Requests()
	if len(reqs) != 5 || reqs[0].Token != "01" || reqs[0].Topic != "com.apple.mgmt.External.test" || reqs[0].PushType != "mdm" || reqs[0].Priority != "10" || reqs[0].Magic != "magic-ok" {
		t.Fatalf("requests %+v", reqs)
	}
	// 503 without Retry-After falls back to 30s.
	srv.ScriptToken(busy, pushtest.Script{Status: 503})
	res, _ = c.Push(ctx, []push.Target{target("busy", busy)})
	if r := res[targets[3].ID]; r.RetryAfter != 30*time.Second {
		t.Errorf("503 default backoff: %+v", r)
	}
	// Cancelled context stops the batch.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Push(cctx, targets[:1]); err == nil {
		t.Error("cancelled context should fail")
	}
}

func TestPerTopicClientsAndExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, _ := testpki.NewCA("apns")
	srv := pushtest.NewServer()
	t.Cleanup(srv.Close)
	fake := clock.NewFake(time.Now())
	valid := pushCert(t, ca, time.Now().Add(-time.Hour))
	store := push.StaticCertStore{"com.apple.mgmt.External.test": valid}
	builds := 0
	transport := func(tls.Certificate) *http.Client { builds++; return srv.Client() }
	c := apns.New(store, apns.WithHost(srv.URL), apns.WithTransport(transport), apns.WithClock(fake), apns.WithTimeout(time.Second))
	for range 3 {
		if res, _ := c.Push(ctx, []push.Target{target("a", []byte{1})}); !res[target("a", nil).ID].Sent {
			t.Fatal("push failed")
		}
	}
	if builds != 1 {
		t.Fatalf("built %d clients, want 1 (cached per topic)", builds)
	}
	// Rotated certificate rebuilds the client.
	store["com.apple.mgmt.External.test"] = pushCert(t, ca, time.Now().Add(-time.Minute))
	if _, err := c.Push(ctx, []push.Target{target("a", []byte{1})}); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("built %d clients after rotation, want 2", builds)
	}
	// Expired certificate is refused before sending.
	fake.Advance(48 * time.Hour)
	res, _ := c.Push(ctx, []push.Target{target("a", []byte{1})})
	if r := res[target("a", nil).ID]; !errors.Is(r.Err, push.ErrCertExpired) {
		t.Fatalf("expired: %+v", r)
	}
	// Missing topic, empty certificate, and unparsable leaf.
	res, _ = c.Push(ctx, []push.Target{{ID: mdm.EnrollmentID{ID: "x"}, Push: mdm.Push{Topic: "other", Token: []byte{1}, Magic: "m"}}})
	if r := res[mdm.EnrollmentID{ID: "x"}]; !errors.Is(r.Err, push.ErrNoCertificate) {
		t.Fatalf("missing topic: %+v", r)
	}
	store["empty"] = tls.Certificate{}
	res, _ = c.Push(ctx, []push.Target{{ID: mdm.EnrollmentID{ID: "e"}, Push: mdm.Push{Topic: "empty", Token: []byte{1}, Magic: "m"}}})
	if r := res[mdm.EnrollmentID{ID: "e"}]; !errors.Is(r.Err, push.ErrNoCertificate) {
		t.Fatalf("empty cert: %+v", r)
	}
	store["garbage"] = tls.Certificate{Certificate: [][]byte{{1, 2, 3}}}
	res, _ = c.Push(ctx, []push.Target{{ID: mdm.EnrollmentID{ID: "g"}, Push: mdm.Push{Topic: "garbage", Token: []byte{1}, Magic: "m"}}})
	if r := res[mdm.EnrollmentID{ID: "g"}]; r.Err == nil {
		t.Fatal("garbage cert should fail")
	}
	// No Leaf set: parsed from DER.
	noLeaf := valid
	noLeaf.Leaf = nil
	fresh := clock.NewFake(time.Now())
	c2 := apns.New(push.StaticCertStore{"com.apple.mgmt.External.test": noLeaf}, apns.WithHost(srv.URL), apns.WithTransport(transport), apns.WithClock(fresh))
	if res, _ := c2.Push(ctx, []push.Target{target("a", []byte{1})}); !res[target("a", nil).ID].Sent {
		t.Fatal("no-leaf cert should work")
	}
	// Unreachable host.
	c3 := apns.New(store, apns.WithHost("https://127.0.0.1:1"), apns.WithClock(fresh), apns.WithTimeout(200*time.Millisecond))
	res, _ = c3.Push(ctx, []push.Target{target("a", []byte{1})})
	if r := res[target("a", nil).ID]; !errors.Is(r.Err, push.ErrUpstream) {
		t.Fatalf("unreachable: %+v", r)
	}
}

func TestTopicFromCert(t *testing.T) {
	t.Parallel()
	uid := asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}
	cert := &x509.Certificate{Subject: pkix.Name{Names: []pkix.AttributeTypeAndValue{{Type: uid, Value: "com.apple.mgmt.External.abc"}}}}
	if topic, err := apns.TopicFromCert(cert); err != nil || topic != "com.apple.mgmt.External.abc" {
		t.Fatalf("topic = %q %v", topic, err)
	}
	if _, err := apns.TopicFromCert(&x509.Certificate{}); err == nil {
		t.Fatal("no uid")
	}
}
