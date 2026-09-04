package pushnotify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
)

// DefaultCertTTL is how long a StoreCertStore trusts a cached certificate
// before asking the store whether its Version moved.
const DefaultCertTTL = 30 * time.Second

// cachedCert is one topic's parsed certificate with the stored Version it
// came from and the time the store last confirmed that Version.
type cachedCert struct {
	cert      tls.Certificate
	version   int64
	checkedAt time.Time
}

// StoreCertStore serves push certificates from a storage.PushCertStore with
// a per-topic cache revalidated against the stored Version once per TTL
// (decision record 0015). A renewal written through StorePushCert bumps the
// Version, so it is picked up within one TTL without a query per push, and a
// failed reload returns an error rather than silently keeping the old
// certificate.
type StoreCertStore struct {
	store storage.PushCertStore
	clock clock.Clock
	ttl   time.Duration

	mu    sync.Mutex
	cache map[string]cachedCert
}

var _ push.CertStore = (*StoreCertStore)(nil)

// CertStoreOption configures a StoreCertStore.
type CertStoreOption func(*StoreCertStore)

// WithCertTTL sets how long a cached certificate is served before its
// Version is checked again (default DefaultCertTTL). A non-positive TTL
// revalidates on every call, which reproduces a per-push staleness check.
func WithCertTTL(d time.Duration) CertStoreOption {
	return func(c *StoreCertStore) { c.ttl = d }
}

// WithCertClock sets the clock used for the TTL (tests).
func WithCertClock(cl clock.Clock) CertStoreOption {
	return func(c *StoreCertStore) { c.clock = cl }
}

// NewStoreCertStore returns a push.CertStore backed by s.
func NewStoreCertStore(s storage.PushCertStore, opts ...CertStoreOption) *StoreCertStore {
	c := &StoreCertStore{store: s, clock: clock.Real{}, ttl: DefaultCertTTL, cache: map[string]cachedCert{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// PushCertificate implements push.CertStore. A cached certificate is returned as
// is inside the TTL. After the TTL the stored Version is read: when it is
// unchanged the cache entry is kept for another TTL, otherwise the PEM pair
// is loaded and parsed again. A topic the store does not know maps to
// push.ErrNoCertificate. The mutex is held only around cache reads and writes,
// never across a storage call.
func (c *StoreCertStore) PushCertificate(ctx context.Context, topic string) (tls.Certificate, error) {
	now := c.clock.Now()
	c.mu.Lock()
	entry, cached := c.cache[topic]
	c.mu.Unlock()
	if cached {
		if c.ttl > 0 && now.Sub(entry.checkedAt) < c.ttl {
			return entry.cert, nil
		}
		version, err := c.store.PushCertVersion(ctx, topic)
		if err != nil {
			return tls.Certificate{}, certStoreError(topic, "version", err)
		}
		if version == entry.version {
			entry.checkedAt = now
			c.mu.Lock()
			c.cache[topic] = entry
			c.mu.Unlock()
			return entry.cert, nil
		}
	}
	rec, err := c.store.PushCert(ctx, topic)
	if err != nil {
		return tls.Certificate{}, certStoreError(topic, "load", err)
	}
	p, err := pushcert.Parse(rec.CertPEM, rec.KeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("push: push certificate for %s: %w", topic, err)
	}
	c.mu.Lock()
	c.cache[topic] = cachedCert{cert: p.TLS, version: rec.Version, checkedAt: now}
	c.mu.Unlock()
	return p.TLS, nil
}

// certStoreError maps a storage error to the package's sentinel for an
// unknown topic and wraps anything else with the operation that failed.
func certStoreError(topic, op string, err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("%w: %s", push.ErrNoCertificate, topic)
	}
	return fmt.Errorf("push: %s push certificate for %s: %w", op, topic, err)
}

// ExpiringCerts lists the stored certificates whose NotAfter is within
// `within` of now, or already past, so a deployment can schedule its own
// renewal check without a timer inside the library. The records carry no
// key material. An empty store yields an empty, non-nil slice.
func ExpiringCerts(ctx context.Context, s storage.PushCertStore, now time.Time, within time.Duration) ([]storage.PushCert, error) {
	certs, err := s.PushCerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("push: list push certificates: %w", err)
	}
	cutoff := now.Add(within)
	out := make([]storage.PushCert, 0, len(certs))
	for _, c := range certs {
		if !c.NotAfter.After(cutoff) {
			out = append(out, c)
		}
	}
	return out, nil
}
