package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

const (
	HeaderID        = "webhook-id"
	HeaderTimestamp = "webhook-timestamp"
	HeaderSignature = "webhook-signature"
)

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return ErrInvalid
	}
	return nil
}

func newHTTPClient(cfg Config) (*http.Client, error) {
	allowed := []netip.Prefix{}
	for _, raw := range cfg.PrivateNetworks {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, ErrInvalid
		}
		allowed = append(allowed, p)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrInvalid
	}
	transport := base.Clone()
	if cfg.Client != nil && cfg.Client.Transport != nil {
		custom, ok := cfg.Client.Transport.(*http.Transport)
		if !ok {
			return nil, ErrInvalid
		}
		transport = custom.Clone()
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		return nil, ErrInvalid
	}
	transport.TLSClientConfig.ServerName = ""
	if cfg.RootCAFile != "" {
		b, err := os.ReadFile(cfg.RootCAFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(b) {
			return nil, ErrInvalid
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	transport.Proxy = nil
	transport.DialTLSContext = nil
	transport.DialTLS = nil //nolint:staticcheck // Clear the deprecated hook because it otherwise bypasses the guarded DialContext.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrInvalid
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("webhook: receiver resolution failed")
		}
		if len(ips) == 0 {
			return nil, ErrInvalid
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			permitted := ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
			for _, p := range allowed {
				permitted = permitted || p.Contains(ip)
			}
			if !permitted {
				return nil, errors.New("webhook: receiver address outside outbound policy")
			}
		}
		var last error
		dialer := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last != nil {
			return nil, errors.New("webhook: receiver connection failed")
		}
		return nil, ErrInvalid
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func signingKey(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) != 32 {
		return nil, ErrInvalid
	}
	return key, nil
}

// Sign follows Standard Webhooks: id.timestamp.body with a v1 HMAC-SHA256.
func Sign(secret, id string, at time.Time, body []byte) (string, error) {
	key, err := signingKey(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + strconv.FormatInt(at.Unix(), 10) + "."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify authenticates the exact request bytes within a five-minute freshness
// window. Receivers must also persist delivery IDs to deduplicate retries.
func Verify(secret string, headers http.Header, body []byte, now time.Time) error {
	if len(headers.Values(HeaderID)) != 1 || len(headers.Values(HeaderTimestamp)) != 1 || len(headers.Values(HeaderSignature)) != 1 {
		return ErrForbidden
	}
	id := headers.Get(HeaderID)
	if id == "" || len(id) > 64 {
		return ErrForbidden
	}
	seconds, err := strconv.ParseInt(headers.Get(HeaderTimestamp), 10, 64)
	if err != nil {
		return ErrForbidden
	}
	at := time.Unix(seconds, 0)
	if at.Before(now.Add(-5*time.Minute)) || at.After(now.Add(5*time.Minute)) {
		return ErrForbidden
	}
	expected, err := Sign(secret, id, at, body)
	if err != nil {
		return err
	}
	for _, signature := range strings.Fields(headers.Get(HeaderSignature)) {
		if hmac.Equal([]byte(signature), []byte(expected)) {
			return nil
		}
	}
	return ErrForbidden
}

// Resolve integrates native deliveries with the existing persistent worker.
func (s *Store) Resolve(_ context.Context, dest string) (eventstore.Sender, error) {
	if !isDestination(dest) {
		return nil, nil
	}
	return func(ctx context.Context, rec eventsink.Record) error { return s.Send(ctx, rec.EventID) }, nil
}

func (s *Store) Send(ctx context.Context, id string) error {
	r, d, err := s.retained(ctx, id)
	if errors.Is(err, ErrExpired) || errors.Is(err, ErrNotFound) {
		return eventstore.ErrExpired
	}
	if err != nil {
		return &eventstore.SourceError{Err: err}
	}
	sub, err := s.load(ctx, d.SubscriptionID, false)
	if err != nil {
		return &eventstore.SourceError{Err: err}
	}
	if sub.Deleted {
		return eventstore.ErrCancelled
	}
	if !sub.Enabled || sub.Paused || sub.Revision != d.Revision {
		return eventstore.ErrPaused
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(r.Body))
	if err != nil {
		return &eventstore.SourceError{Err: err}
	}
	now := s.cfg.Now()
	sig, err := Sign(sub.Key, id, now, r.Body)
	if err != nil {
		return &eventstore.SourceError{Err: err}
	}
	if sub.PreviousKey != "" && now.Before(sub.PreviousUntil) {
		previous, err := Sign(sub.PreviousKey, id, now, r.Body)
		if err != nil {
			return &eventstore.SourceError{Err: err}
		}
		sig += " " + previous
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderID, id)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(HeaderSignature, sig)
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errors.New("webhook: delivery transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var delay time.Duration
	if seconds, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil {
		if seconds > 0 {
			delay = time.Duration(min(seconds, 86400)) * time.Second
		}
	} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
		delay = max(time.Duration(0), min(at.Sub(now), 24*time.Hour))
	}
	return &eventsink.HTTPError{Status: resp.StatusCode, RetryAfter: delay}
}

// PayloadHandler authenticates only subscription-scoped receiver credentials.
// It accepts no administrator tokens and never grants administrative access.
func (s *Store) PayloadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := strings.Split(strings.TrimPrefix(r.URL.Path, PayloadPath), "/")
		if len(path) != 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id, part := path[0], path[1]
		list, err := s.Deliveries(r.Context(), id, "", 1)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if len(list) != 1 || list[0].ID != id {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		d := list[0]
		sub, err := s.load(r.Context(), d.SubscriptionID, false)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if err != nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !sub.Enabled || sub.Deleted || sub.Revision != d.Revision || subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(sub.TokenHash)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		retained, _, err := s.retained(r.Context(), id)
		if errors.Is(err, ErrExpired) {
			w.WriteHeader(http.StatusGone)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		p, ok := retained.Parts[part]
		if !ok || !sub.Payload.allows(part) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := payloadBytes(p)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", p.ContentType)
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Security-Policy", "sandbox")
		_, _ = w.Write(body)
	})
}
