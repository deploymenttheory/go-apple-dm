package apns

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/push"
	"github.com/deploymenttheory/go-apple-dm/pushcert"
)

// Hosts.
const (
	ProductionHost  = "https://api.push.apple.com"
	DevelopmentHost = "https://api.development.push.apple.com"
)

// Client sends MDM pushes.
type Client struct {
	certs   push.CertStore
	host    string
	clock   clock.Clock
	timeout time.Duration
	// transport builds the HTTP client for a topic; tests override it.
	transport func(cert tls.Certificate) *http.Client

	mu      sync.Mutex
	clients map[string]*topicClient
}

type topicClient struct {
	client *http.Client
	expiry time.Time
	leaf   *x509.Certificate
}

// Option configures the client.
type Option func(*Client)

// WithHost overrides the APNs host (DevelopmentHost or a test server).
func WithHost(h string) Option { return func(c *Client) { c.host = h } }

// WithClock sets the clock used for certificate expiry checks.
func WithClock(cl clock.Clock) Option { return func(c *Client) { c.clock = cl } }

// WithTimeout sets the per-request timeout (default 20s).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithTransport replaces how per-topic HTTP clients are built (tests).
func WithTransport(f func(cert tls.Certificate) *http.Client) Option {
	return func(c *Client) { c.transport = f }
}

// New creates a client that fetches push certificates from certs.
func New(certs push.CertStore, opts ...Option) *Client {
	c := &Client{certs: certs, host: ProductionHost, clock: clock.Real{}, timeout: 20 * time.Second, clients: map[string]*topicClient{}}
	c.transport = c.defaultTransport
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) defaultTransport(cert tls.Certificate) *http.Client {
	return &http.Client{
		Timeout: c.timeout,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: true,
		},
	}
}

// clientFor returns the HTTP client for a topic, loading the certificate on
// first use or after it changed; an expired certificate is an error.
func (c *Client) clientFor(ctx context.Context, topic string) (*http.Client, error) {
	cert, err := c.certs.PushCertificate(ctx, topic)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("%w: %s", push.ErrNoCertificate, topic)
	}
	leaf := cert.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("push: parse push certificate: %w", err)
		}
	}
	now := c.clock.Now()
	if now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("%w: %s expired %s", push.ErrCertExpired, topic, leaf.NotAfter.Format(time.RFC3339))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tc, ok := c.clients[topic]; ok && tc.leaf.Equal(leaf) {
		return tc.client, nil
	}
	tc := &topicClient{client: c.transport(cert), expiry: leaf.NotAfter, leaf: leaf}
	c.clients[topic] = tc
	return tc.client, nil
}

// Push implements push.Pusher.
func (c *Client) Push(ctx context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	out := make(map[mdm.EnrollmentID]push.Result, len(targets))
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("push: %w", err)
		}
		out[t.ID] = c.pushOne(ctx, t)
	}
	return out, nil
}

// apnsError is the JSON body APNs returns on failure.
type apnsError struct {
	Reason    string `json:"reason"`
	Timestamp int64  `json:"timestamp"`
}

func (c *Client) pushOne(ctx context.Context, t push.Target) push.Result {
	if !t.Push.Valid() {
		return push.Result{Outcome: push.OutcomeSkipped, Err: fmt.Errorf("%w: incomplete push info", push.ErrInvalidToken)}
	}
	client, err := c.clientFor(ctx, t.Push.Topic)
	if err != nil {
		return push.Result{Err: err}
	}
	body, err := json.Marshal(map[string]string{"mdm": t.Push.Magic})
	if err != nil {
		return push.Result{Err: fmt.Errorf("push: %w", err)}
	}
	url := c.host + "/3/device/" + hex.EncodeToString(t.Push.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return push.Result{Err: fmt.Errorf("push: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", t.Push.Topic)
	req.Header.Set("apns-push-type", "mdm")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", "0")
	resp, err := client.Do(req)
	if err != nil {
		return push.Result{Outcome: push.OutcomeUnavailable, Err: fmt.Errorf("%w: %w", push.ErrUpstream, err)}
	}
	defer resp.Body.Close()
	r := push.Result{Status: resp.StatusCode, APNSID: resp.Header.Get("apns-id")}
	if resp.StatusCode == http.StatusOK {
		r.Outcome = push.OutcomeSent
		return r
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ae apnsError
	if json.Unmarshal(data, &ae) == nil {
		r.Reason = ae.Reason
	}
	r.Outcome = Classify(resp.StatusCode, ae.Reason)
	switch r.Outcome {
	case push.OutcomeInvalidToken:
		r.Err = fmt.Errorf("%w: %d %s", push.ErrInvalidToken, resp.StatusCode, ae.Reason)
	case push.OutcomeRejected:
		r.Err = fmt.Errorf("%w: %d %s", push.ErrRejected, resp.StatusCode, ae.Reason)
	case push.OutcomeRateLimited:
		r.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		r.Err = fmt.Errorf("%w: %d %s", push.ErrRateLimited, resp.StatusCode, ae.Reason)
	case push.OutcomeUnavailable, push.OutcomeSent, push.OutcomeSkipped:
		r.Err = fmt.Errorf("%w: %d %s", push.ErrUpstream, resp.StatusCode, ae.Reason)
	}
	return r
}

// Classify turns an APNs status and reason into an outcome. It is exported
// because Result records Status and Reason, so a caller holding a stored
// result — or implementing its own Pusher — can reach the same verdict
// without restating the table.
//
// The status decides, because it is always present and Apple pairs each
// reason with exactly one status. Apple states that a push should not be
// repeated only for 410: "there is no need to send further pushes to the
// same device token". The 400 and 403 families are the sender's problem —
// BadDeviceToken is documented as "verify that the request contains a valid
// token *and that the token matches the environment*", so a sandbox
// certificate against production tokens rejects every device in the fleet
// with a reason that says nothing about any of them.
//
// IdleTimeout is the one 400 that is not permanent: it is the HTTP/2
// connection going idle, not a fault in the request.
func Classify(status int, reason string) push.Outcome {
	switch {
	case status == http.StatusGone:
		return push.OutcomeInvalidToken
	case status == http.StatusTooManyRequests, status == http.StatusServiceUnavailable:
		return push.OutcomeRateLimited
	case reason == ReasonIdleTimeout:
		return push.OutcomeUnavailable
	case status == http.StatusBadRequest,
		status == http.StatusForbidden,
		status == http.StatusNotFound,
		status == http.StatusMethodNotAllowed,
		status == http.StatusRequestEntityTooLarge:
		return push.OutcomeRejected
	}
	return push.OutcomeUnavailable
}

// DefaultRetryAfter is a back-off for a caller that wants one when APNs
// asked for nothing. It is not applied here: Result.RetryAfter reports what
// Apple said, and zero means Apple said nothing, so a caller can tell the
// difference between a stated pause and an assumed one.
const DefaultRetryAfter = 30 * time.Second

// retryAfter parses a Retry-After header in seconds, returning zero when the
// header is absent or unparsable.
func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

// TopicFromCert returns the push topic embedded in an APNs push
// certificate's subject UID (OID 0.9.2342.19200300.100.1.1). It forwards to
// pushcert.TopicFromCert and is kept for compatibility.
func TopicFromCert(cert *x509.Certificate) (string, error) {
	return pushcert.TopicFromCert(cert)
}
