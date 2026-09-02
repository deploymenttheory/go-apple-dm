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

	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/push/pushcert"
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
		return push.Result{Err: fmt.Errorf("%w: incomplete push info", push.ErrInvalidToken), Invalid: true}
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
		return push.Result{Err: fmt.Errorf("%w: %w", push.ErrUpstream, err)}
	}
	defer resp.Body.Close()
	r := push.Result{Status: resp.StatusCode, APNSID: resp.Header.Get("apns-id")}
	if resp.StatusCode == http.StatusOK {
		r.Sent = true
		return r
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ae apnsError
	if json.Unmarshal(data, &ae) == nil {
		r.Reason = ae.Reason
	}
	switch {
	case resp.StatusCode == http.StatusGone, ae.Reason == "BadDeviceToken", ae.Reason == "DeviceTokenNotForTopic", ae.Reason == "Unregistered":
		r.Invalid = true
		r.Err = fmt.Errorf("%w: %d %s", push.ErrInvalidToken, resp.StatusCode, ae.Reason)
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
		r.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		r.Err = fmt.Errorf("%w: %d %s", push.ErrRateLimited, resp.StatusCode, ae.Reason)
	default:
		r.Err = fmt.Errorf("%w: %d %s", push.ErrUpstream, resp.StatusCode, ae.Reason)
	}
	return r
}

// retryAfter parses a Retry-After header (seconds); default 30s when absent
// or unparsable.
func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 30 * time.Second
}

// TopicFromCert returns the push topic embedded in an APNs push
// certificate's subject UID (OID 0.9.2342.19200300.100.1.1). It forwards to
// pushcert.TopicFromCert and is kept for compatibility.
func TopicFromCert(cert *x509.Certificate) (string, error) {
	return pushcert.TopicFromCert(cert)
}
