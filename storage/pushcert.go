package storage

import (
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/push/pushcert"
)

// ValidatePushCert checks a PEM certificate and key pair the way every
// backend's StorePushCert must: the key matches the certificate, the
// subject carries an APNs topic, the topic matches when one is given, and
// the certificate is valid at the given time. It returns the record to
// store, with copies of the PEM bytes and Version unset.
func ValidatePushCert(topic string, certPEM, keyPEM []byte, at time.Time) (PushCert, error) {
	p, err := pushcert.Parse(certPEM, keyPEM)
	if err != nil {
		return PushCert{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if topic != "" && topic != p.Topic {
		return PushCert{}, fmt.Errorf("%w: certificate topic %q does not match %q", ErrInvalid, p.Topic, topic)
	}
	if at.After(p.NotAfter) {
		return PushCert{}, fmt.Errorf("%w: certificate for %s expired %s", ErrInvalid, p.Topic, p.NotAfter.UTC().Format(time.RFC3339))
	}
	if at.Before(p.NotBefore) {
		return PushCert{}, fmt.Errorf("%w: certificate for %s not valid before %s", ErrInvalid, p.Topic, p.NotBefore.UTC().Format(time.RFC3339))
	}
	return PushCert{
		Topic:    p.Topic,
		CertPEM:  append([]byte(nil), certPEM...),
		KeyPEM:   append([]byte(nil), keyPEM...),
		NotAfter: p.NotAfter.UTC(),
	}, nil
}

// ErrUserChannelRequired is wrapped in ErrInvalid by UserAuthStore methods
// called with a device channel.
var ErrUserChannelRequired = errors.New("storage: user channel required")
