package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/v3/server/pushnotify"
)

// Push certificate sources.
const (
	// PushSourceOff sends no pushes. A declaration change still queues a
	// command, which the device collects on its next check-in.
	PushSourceOff = "off"
	// PushSourceFile loads one certificate and key from disk.
	PushSourceFile = "file"
	// PushSourceStore loads certificates from the push certificate store,
	// reloading when an admin uploads a new one.
	PushSourceStore = "store"
)

// DefaultPushCoalesce collapses repeated pushes to one enrollment inside this
// window, so a burst of declaration changes wakes a device once.
const DefaultPushCoalesce = 5 * time.Second

// PushConfig selects where APNs credentials come from.
//
// With no source the notifier is built without a pusher, so a declaration
// change queues a command and never wakes the device. The device collects it
// on its next check-in, which for an idle Mac can be hours.
type PushConfig struct {
	// Source is off, file, or store.
	Source string
	// CertFile and KeyFile are the PEM pair for the file source.
	CertFile, KeyFile string
	// Topic is the APNs topic for the file source. Empty derives it from the
	// certificate, which is what an operator should prefer: the topic lives
	// in the certificate's subject and typing it by hand is how it goes
	// wrong.
	Topic string
	// Host overrides the APNs endpoint, for the lab and for tests.
	Host string
	// Coalesce is the window repeated pushes collapse into; zero uses
	// DefaultPushCoalesce, negative disables coalescing.
	Coalesce time.Duration
	// CertTTL is how long a store-backed certificate is cached before its
	// version is rechecked.
	CertTTL time.Duration
	// Transport overrides the HTTP client per certificate, for tests.
	Transport func(tls.Certificate) *http.Client
	// Pusher overrides everything, for tests and for an embedder with its
	// own APNs path.
	Pusher push.Pusher
}

// Enabled reports whether pushes are sent.
func (p PushConfig) Enabled() bool {
	return p.Pusher != nil || (p.Source != "" && p.Source != PushSourceOff)
}

func (p PushConfig) validate() error {
	switch p.Source {
	case "", PushSourceOff, PushSourceStore:
		return nil
	case PushSourceFile:
		if p.CertFile == "" || p.KeyFile == "" {
			return fmt.Errorf("%w: push source %q needs a certificate and key", ErrConfig, p.Source)
		}
		return nil
	default:
		return fmt.Errorf("%w: push source %q (want off, file, or store)", ErrConfig, p.Source)
	}
}

// wirePush builds the push notifier. It returns nil when pushes are off, and
// ddmsync.Notifier treats a nil pusher as "consider it delivered", so the change
// rows still drain.
func (a *App) wirePush() (*pushnotify.Notifier, error) {
	cfg := a.cfg.Push
	if !cfg.Enabled() {
		return nil, nil
	}
	pusher := cfg.Pusher
	if pusher == nil {
		certs, err := a.pushCertStore(cfg)
		if err != nil {
			return nil, err
		}
		opts := []apns.Option{}
		if cfg.Host != "" {
			opts = append(opts, apns.WithHost(cfg.Host))
		}
		if cfg.Transport != nil {
			opts = append(opts, apns.WithTransport(cfg.Transport))
		}
		pusher = apns.New(certs, opts...)
	}
	window := cfg.Coalesce
	if window == 0 {
		window = DefaultPushCoalesce
	}
	if window > 0 {
		pusher = push.Coalesce(pusher, window, a.cfg.Clock)
	}
	return &pushnotify.Notifier{Store: a.Store, Pusher: pusher, Bus: a.cfg.Bus, Clock: a.cfg.Clock}, nil
}

// pushCertStore resolves the certificate source.
func (a *App) pushCertStore(cfg PushConfig) (push.CertStore, error) {
	if cfg.Source == PushSourceFile {
		pair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("%w: push certificate: %w", ErrConfig, err)
		}
		topic := cfg.Topic
		if topic == "" {
			var terr error
			topic, terr = topicOf(pair.Leaf)
			if terr != nil {
				return nil, terr
			}
		}
		return push.StaticCertStore{topic: pair}, nil
	}
	opts := []pushnotify.CertStoreOption{pushnotify.WithCertClock(a.cfg.Clock)}
	if cfg.CertTTL > 0 {
		opts = append(opts, pushnotify.WithCertTTL(cfg.CertTTL))
	}
	return pushnotify.NewStoreCertStore(a.Store, opts...), nil
}

// topicOf reads the topic out of the certificate, so an operator never types
// it: the topic lives in the certificate's subject, and a hand-typed one that
// disagrees means APNs refuses every push.
//
// tls.LoadX509KeyPair has populated Leaf since Go 1.23, so the nil check is a
// guard against a caller passing a hand-built pair rather than a path the
// loader can take.
func topicOf(leaf *x509.Certificate) (string, error) {
	if leaf == nil {
		return "", fmt.Errorf("%w: push certificate has no leaf", ErrConfig)
	}
	topic, err := pushcert.TopicFromCert(leaf)
	if err != nil {
		return "", fmt.Errorf("%w: push certificate: %w", ErrConfig, err)
	}
	return topic, nil
}
