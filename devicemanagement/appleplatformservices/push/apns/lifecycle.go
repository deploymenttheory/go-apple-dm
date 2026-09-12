package apns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
)

// ErrClosed means the client has been shut down. Create a new client to resume.
var ErrClosed = errors.New("apns: client closed")

// Close stops new sends, cancels active requests and retires every connection
// pool. It is idempotent. A replaced certificate's pool is closed the same way.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for topic, tc := range c.clients {
		closeClient(tc.client)
		delete(c.clients, topic)
	}
	return nil
}

func closeClient(c *http.Client) {
	if t, ok := c.Transport.(*managedTransport); ok {
		t.close()
	}
}

// Track requests until their response bodies close, not just until headers
// arrive. Canceling HTTP/2 streams plus closing idle connections releases the
// old TLS identity even if renewal races an active send.
type managedTransport struct {
	base   http.RoundTripper
	mu     sync.Mutex
	closed bool
	active map[*http.Request]*activeRequest
}

type activeRequest struct {
	cancel context.CancelFunc
	conn   net.Conn
}

func newManagedTransport(base http.RoundTripper) *managedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &managedTransport{base: base, active: map[*http.Request]*activeRequest{}}
}

func (t *managedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	active := &activeRequest{cancel: cancel}
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			active.conn = info.Conn
			closed := t.closed
			t.mu.Unlock()
			if closed {
				_ = info.Conn.Close()
			}
		},
	})
	r := req.Clone(ctx)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel()
		return nil, ErrClosed
	}
	t.active[r] = active
	t.mu.Unlock()
	release := func() {
		cancel()
		t.mu.Lock()
		delete(t.active, r)
		closed, conn := t.closed, active.conn
		t.mu.Unlock()
		if closed {
			if conn != nil {
				_ = conn.Close()
			}
			t.CloseIdleConnections()
		}
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		release()
		return nil, fmt.Errorf("apns: transport: %w", err)
	}
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}

func (t *managedTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

func (t *managedTransport) close() {
	t.mu.Lock()
	t.closed = true
	connections := make([]net.Conn, 0, len(t.active))
	for _, active := range t.active {
		active.cancel()
		if active.conn != nil {
			connections = append(connections, active.conn)
		}
	}
	t.mu.Unlock()
	// HTTP/2 stream cancellation can finish before the transport marks its
	// connection idle. Close the observed active connections as well, so
	// retirement cannot miss that transition. GotConn handles concurrent dials.
	for _, conn := range connections {
		_ = conn.Close()
	}
	t.CloseIdleConnections()
}

type releaseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	if err != nil {
		return fmt.Errorf("apns: close response: %w", err)
	}
	return nil
}

// Retire closes connections using a topic's current identity. The next send
// resolves the certificate store again, including when the same PEM is renewed.
func (c *Client) Retire(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tc, ok := c.clients[topic]; ok {
		closeClient(tc.client)
		delete(c.clients, topic)
	}
}

// Retire releases one app topic's connections after a credential update.
func (c *AppClient) Retire(topic string) { c.client.Retire(topic) }
