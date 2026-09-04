package proxyclient_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/internal/proxywire"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/proxyserver"
	ddminmem "github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
)

// dmCheckin builds a DeclarativeManagement check-in from the schema type
// and decodes it the way the transport would.
func dmCheckin(t *testing.T, udid, endpoint string, data []byte) (*mdm.Checkin, *checkin.DeclarativeManagement) {
	t.Helper()
	raw, err := plist.Marshal(checkin.DeclarativeManagement{MessageType: "DeclarativeManagement", UDID: udid, Endpoint: endpoint, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	ck, err := mdm.DecodeCheckin(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := ck.Message.(*checkin.DeclarativeManagement)
	if !ok {
		t.Fatalf("message is %T", ck.Message)
	}
	return ck, m
}

// capture is a fake ddm role that records the request and answers as told.
type capture struct {
	status  int
	body    []byte
	headers map[string]string
	mu      sync.Mutex
	req     *http.Request
	raw     []byte
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.req, c.raw = r.Clone(context.Background()), body
	c.mu.Unlock()
	for k, v := range c.headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body)
}

func (c *capture) seen() (*http.Request, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.req, c.raw
}

func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func mustHandler(t *testing.T, cfg proxyclient.Config) service.DMHandler {
	t.Helper()
	h, err := proxyclient.Handler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHandler(t *testing.T) {
	t.Parallel()
	t.Run("EmptyURL", func(t *testing.T) {
		t.Parallel()
		if _, err := proxyclient.Handler(proxyclient.Config{}); !errors.Is(err, proxyclient.ErrBadURL) {
			t.Fatalf("empty URL: %v", err)
		}
	})
	t.Run("BadURL", func(t *testing.T) {
		t.Parallel()
		for _, u := range []string{"://nope", "ftp://ddm.example", "ddm.example", "/v1", "http://", "http://ddm.example/%zz"} {
			if _, err := proxyclient.Handler(proxyclient.Config{URL: u}); !errors.Is(err, proxyclient.ErrBadURL) {
				t.Errorf("%q: %v", u, err)
			}
		}
	})
	t.Run("OK", func(t *testing.T) {
		t.Parallel()
		for _, u := range []string{"http://ddm.example", "https://ddm.example:8443/prefix/", "http://127.0.0.1:1"} {
			if _, err := proxyclient.Handler(proxyclient.Config{URL: u}); err != nil {
				t.Errorf("%q: %v", u, err)
			}
		}
	})
}

func TestForward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("RawPlistForwarded", func(t *testing.T) {
		t.Parallel()
		c := &capture{status: 200, body: []byte(`{}`)}
		srv := serve(t, c)
		// A trailing slash and a prefix are tolerated (nothing is lost).
		h := mustHandler(t, proxyclient.Config{URL: srv.URL + "/ddm/"})
		ck, m := dmCheckin(t, "D1", "declaration/configuration/x", nil)
		if _, err := h(ctx, &mdm.Request{}, ck, m); err != nil {
			t.Fatal(err)
		}
		req, raw := c.seen()
		if !bytes.Equal(raw, ck.Raw) {
			t.Fatalf("forwarded %q, want the raw check-in %q", raw, ck.Raw)
		}
		if req.Method != http.MethodPost || req.URL.Path != "/ddm"+proxywire.Path {
			t.Fatalf("%s %s", req.Method, req.URL.Path)
		}
		if req.Header.Get(proxywire.HeaderSignature) != "" || req.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected headers: %v", req.Header)
		}
		// The server can decode what it received exactly as the device path does.
		got, err := mdm.DecodeCheckin(raw)
		if err != nil || got.ID != ck.ID || got.Type != "DeclarativeManagement" {
			t.Fatalf("decode forwarded: %+v %v", got, err)
		}
	})
	t.Run("Signed", func(t *testing.T) {
		t.Parallel()
		c := &capture{status: 200, body: []byte(`{}`)}
		srv := serve(t, c)
		key := []byte("send")
		h := mustHandler(t, proxyclient.Config{URL: srv.URL, SendKey: key, Client: srv.Client()})
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		if _, err := h(ctx, &mdm.Request{}, ck, m); err != nil {
			t.Fatal(err)
		}
		req, raw := c.seen()
		if err := proxywire.Verify(key, req.Header.Get(proxywire.HeaderSignature), raw); err != nil {
			t.Fatalf("request signature: %v", err)
		}
	})
	t.Run("ContentType", func(t *testing.T) {
		t.Parallel()
		c := &capture{status: 200, body: []byte(`{}`)}
		srv := serve(t, c)
		h := mustHandler(t, proxyclient.Config{URL: srv.URL})
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		if _, err := h(ctx, &mdm.Request{}, ck, m); err != nil {
			t.Fatal(err)
		}
		req, _ := c.seen()
		if req.Header.Get("Content-Type") != proxywire.ContentType {
			t.Fatalf("content type %q", req.Header.Get("Content-Type"))
		}
	})
	t.Run("AuthHeader", func(t *testing.T) {
		t.Parallel()
		c := &capture{status: 200, body: []byte(`{}`)}
		srv := serve(t, c)
		h := mustHandler(t, proxyclient.Config{URL: srv.URL, Auth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }})
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		if _, err := h(ctx, &mdm.Request{}, ck, m); err != nil {
			t.Fatal(err)
		}
		req, _ := c.seen()
		if req.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("authorization %q", req.Header.Get("Authorization"))
		}
	})
	t.Run("NoRawBytes", func(t *testing.T) {
		t.Parallel()
		c := &capture{status: 200}
		srv := serve(t, c)
		h := mustHandler(t, proxyclient.Config{URL: srv.URL})
		_, m := dmCheckin(t, "D1", "tokens", nil)
		if _, err := h(ctx, &mdm.Request{}, nil, m); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, service.ErrInvalidMessage) {
			t.Fatalf("nil check-in: %v", err)
		}
		if _, err := h(ctx, &mdm.Request{}, &mdm.Checkin{Message: m}, m); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("check-in without raw bytes: %v", err)
		}
		if req, _ := c.seen(); req != nil {
			t.Fatal("request was sent")
		}
	})
}

func TestRelay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	relay := func(t *testing.T, c *capture) (service.DMResponse, error) {
		t.Helper()
		srv := serve(t, c)
		h := mustHandler(t, proxyclient.Config{URL: srv.URL})
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		return h(ctx, &mdm.Request{}, ck, m)
	}
	t.Run("200Body", func(t *testing.T) {
		t.Parallel()
		got, err := relay(t, &capture{status: 200, body: []byte(`{"SyncTokens":{}}`), headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}})
		if err != nil || got.Status != 200 || string(got.Body) != `{"SyncTokens":{}}` || got.ContentType != "application/json; charset=utf-8" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("404Stays404", func(t *testing.T) {
		t.Parallel()
		got, err := relay(t, &capture{status: 404, body: []byte("ignored")})
		if err != nil || got.Status != 404 || got.Body != nil || got.ContentType != "" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("EmptyStatus200", func(t *testing.T) {
		t.Parallel()
		got, err := relay(t, &capture{status: 200})
		if err != nil || got.Status != 200 || len(got.Body) != 0 || got.ContentType != "application/json" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("400IsBadRequest", func(t *testing.T) {
		t.Parallel()
		_, err := relay(t, &capture{status: 400})
		if service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, proxyclient.ErrUpstream) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("5xxIsUpstreamError", func(t *testing.T) {
		t.Parallel()
		for _, status := range []int{500, 502, 503} {
			_, err := relay(t, &capture{status: status, body: []byte("detail")})
			if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
				t.Errorf("%d: %v", status, err)
			}
		}
	})
	t.Run("401IsUpstreamError", func(t *testing.T) {
		t.Parallel()
		for _, status := range []int{401, 403, 405, 413, 415} {
			_, err := relay(t, &capture{status: status})
			if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
				t.Errorf("%d: %v", status, err)
			}
		}
	})
}

func TestSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := []byte("recv")
	body := []byte(`{"SyncTokens":{}}`)
	t.Run("ResponseVerified", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, &capture{status: 200, body: body, headers: map[string]string{proxywire.HeaderSignature: proxywire.SignResponse(key, 200, body)}})
		h := mustHandler(t, proxyclient.Config{URL: srv.URL, RecvKey: key})
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		got, err := h(ctx, &mdm.Request{}, ck, m)
		if err != nil || !bytes.Equal(got.Body, body) {
			t.Fatalf("%+v %v", got, err)
		}
		// Signed 404 verifies over the empty body.
		srv404 := serve(t, &capture{status: 404, headers: map[string]string{proxywire.HeaderSignature: proxywire.SignResponse(key, 404, nil)}})
		h404 := mustHandler(t, proxyclient.Config{URL: srv404.URL, RecvKey: key})
		if got, err := h404(ctx, &mdm.Request{}, ck, m); err != nil || got.Status != 404 {
			t.Fatalf("signed 404: %+v %v", got, err)
		}
	})
	// A signature over the body alone makes the empty-response MAC a constant,
	// so an on-path attacker lifts it from any error response and replays it
	// with a status of their choosing. A forged 404 tells every device it has
	// no declarations.
	t.Run("StatusIsCovered", func(t *testing.T) {
		t.Parallel()
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		// The signature the ddm role emits for an empty 500, replayed on a 404.
		lifted := proxywire.SignResponse(key, 500, nil)
		srv := serve(t, &capture{status: 404, headers: map[string]string{proxywire.HeaderSignature: lifted}})
		h := mustHandler(t, proxyclient.Config{URL: srv.URL, RecvKey: key})
		if _, err := h(ctx, &mdm.Request{}, ck, m); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("replayed signature accepted under another status: %v", err)
		}
		// A genuine 200 body, served as a 404 so relay reports no declarations.
		downgrade := serve(t, &capture{status: 404, body: body, headers: map[string]string{
			proxywire.HeaderSignature: proxywire.SignResponse(key, 200, body),
		}})
		hd := mustHandler(t, proxyclient.Config{URL: downgrade.URL, RecvKey: key})
		if _, err := hd(ctx, &mdm.Request{}, ck, m); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("status downgrade accepted: %v", err)
		}
	})
	t.Run("BadResponseSignature", func(t *testing.T) {
		t.Parallel()
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		for name, hdr := range map[string]string{
			"wrong key":  proxywire.SignResponse([]byte("other"), 200, body),
			"other body": proxywire.SignResponse(key, 200, []byte("{}")),
			"garbage":    "!!",
		} {
			srv := serve(t, &capture{status: 200, body: body, headers: map[string]string{proxywire.HeaderSignature: hdr}})
			h := mustHandler(t, proxyclient.Config{URL: srv.URL, RecvKey: key})
			_, err := h(ctx, &mdm.Request{}, ck, m)
			if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxywire.ErrBadSignature) || !errors.Is(err, proxyclient.ErrUpstream) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Missing is a failure too, even on a status the device would accept.
		srv := serve(t, &capture{status: 404})
		h := mustHandler(t, proxyclient.Config{URL: srv.URL, RecvKey: key})
		if _, err := h(ctx, &mdm.Request{}, ck, m); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxywire.ErrMissingSignature) {
			t.Fatalf("missing: %v", err)
		}
	})
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so the server notices the client hanging up.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-done:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { close(done) })
	h := mustHandler(t, proxyclient.Config{URL: srv.URL, Timeout: 50 * time.Millisecond})
	ck, m := dmCheckin(t, "D1", "tokens", nil)
	start := time.Now()
	_, err := h(context.Background(), &mdm.Request{}, ck, m)
	if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	// The caller's context is honoured as well.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h(ctx, &mdm.Request{}, ck, m); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()
	srv := serve(t, &capture{status: 200, body: bytes.Repeat([]byte("x"), 100)})
	ck, m := dmCheckin(t, "D1", "tokens", nil)
	h := mustHandler(t, proxyclient.Config{URL: srv.URL, MaxBody: 99})
	_, err := h(context.Background(), &mdm.Request{}, ck, m)
	if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxywire.ErrBodyTooLarge) || !errors.Is(err, proxyclient.ErrUpstream) {
		t.Fatalf("over limit: %v", err)
	}
	if got, err := mustHandler(t, proxyclient.Config{URL: srv.URL, MaxBody: 100})(context.Background(), &mdm.Request{}, ck, m); err != nil || len(got.Body) != 100 {
		t.Fatalf("at limit: %d %v", len(got.Body), err)
	}
}

func TestTransportError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(&capture{status: 200})
	url := srv.URL
	srv.Close()
	h := mustHandler(t, proxyclient.Config{URL: url})
	ck, m := dmCheckin(t, "D1", "tokens", nil)
	_, err := h(context.Background(), &mdm.Request{}, ck, m)
	if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
		t.Fatalf("closed server: %v", err)
	}
	// A body that dies mid-read is an upstream failure too.
	dying := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	h = mustHandler(t, proxyclient.Config{URL: dying.URL})
	if _, err := h(context.Background(), &mdm.Request{}, ck, m); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
		t.Fatalf("truncated body: %v", err)
	}
}

// TestRoundTripThroughProxyServer runs a real engine behind proxyserver
// and a real service.Core in front of proxyclient, so the whole split
// deployment is exercised through the DMHandler wiring.
func TestRoundTripThroughProxyServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine, err := ddm.New(ddm.Config{Store: ddminmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.PutDeclaration(ctx, []byte(`{"Type":"com.apple.management.organization-info","Identifier":"org","Payload":{"Name":"Acme"}}`)); err != nil {
		t.Fatal(err)
	}
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	if _, err := engine.AssignDeclaration(ctx, dev, "org"); err != nil {
		t.Fatal(err)
	}
	mdmToDDM, ddmToMDM := []byte("mdm->ddm"), []byte("ddm->mdm")
	ingress, err := proxyserver.Handler(proxyserver.Config{
		Backend: engine, RecvKey: mdmToDDM, SendKey: ddmToMDM,
		Auth:   proxyserver.BearerAuth("tok"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := serve(t, ingress)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }
	egress, err := proxyclient.Handler(proxyclient.Config{URL: srv.URL, SendKey: mdmToDDM, RecvKey: ddmToMDM, Auth: bearer})
	if err != nil {
		t.Fatal(err)
	}
	core, err := service.New(service.Config{Store: inmem.New(), Pinning: service.PinOff, DeclarativeManagement: egress})
	if err != nil {
		t.Fatal(err)
	}
	send := func(fields map[string]any) (*service.CheckinResult, error) {
		raw, err := plist.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		ck, err := mdm.DecodeCheckin(raw)
		if err != nil {
			t.Fatal(err)
		}
		return core.Checkin(ctx, &mdm.Request{}, ck)
	}
	if _, err := send(map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": "D1", "Model": "Mac", "ModelName": "MacBook", "DeviceName": "d", "SerialNumber": "S1"}); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := send(map[string]any{"MessageType": "TokenUpdate", "Topic": "com.apple.mgmt.t", "UDID": "D1", "PushMagic": "magic", "Token": []byte{1, 2, 3}}); err != nil {
		t.Fatalf("TokenUpdate: %v", err)
	}
	dm := func(endpoint string, data []byte) (*service.CheckinResult, error) {
		raw, err := plist.Marshal(checkin.DeclarativeManagement{MessageType: "DeclarativeManagement", UDID: "D1", Endpoint: endpoint, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		ck, err := mdm.DecodeCheckin(raw)
		if err != nil {
			t.Fatal(err)
		}
		return core.Checkin(ctx, &mdm.Request{}, ck)
	}

	tokens, err := dm("tokens", nil)
	if err != nil || tokens.Status != 200 || tokens.ContentType != "application/json" {
		t.Fatalf("tokens: %+v %v", tokens, err)
	}
	want, err := engine.Tokens(ctx, dev)
	if err != nil || !bytes.Equal(tokens.Body, want) {
		t.Fatalf("tokens body %s, engine %s (%v)", tokens.Body, want, err)
	}
	items, err := dm("declaration-items", nil)
	if err != nil || !strings.Contains(string(items.Body), `"Identifier":"org"`) {
		t.Fatalf("declaration-items: %+v %v", items, err)
	}
	decl, err := dm("declaration/management/org", nil)
	if err != nil || decl.Status != 200 || !strings.Contains(string(decl.Body), `"Name":"Acme"`) {
		t.Fatalf("declaration: %+v %v", decl, err)
	}
	gone, err := dm("declaration/management/nope", nil)
	if err != nil || gone.Status != 404 || len(gone.Body) != 0 {
		t.Fatalf("unknown declaration must be 404 for the device: %+v %v", gone, err)
	}
	status, err := dm("status", []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}}}`))
	if err != nil || status.Status != 200 || len(status.Body) != 0 {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := dm("status", nil); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("status without data: %v", err)
	}
	if _, err := dm("nonsense", nil); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("bad endpoint: %v", err)
	}
	// The ddm role checks the request signature: a client with the wrong
	// key is refused and the device sees an internal error, never a 401.
	wrong, err := proxyclient.Handler(proxyclient.Config{URL: srv.URL, SendKey: []byte("wrong"), RecvKey: ddmToMDM, Auth: bearer})
	if err != nil {
		t.Fatal(err)
	}
	ck, m := dmCheckin(t, "D1", "tokens", nil)
	if _, err := wrong(ctx, &mdm.Request{}, ck, m); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
		t.Fatalf("wrong key: %v", err)
	}
	// And the mdm role checks the response signature.
	wrongRecv, err := proxyclient.Handler(proxyclient.Config{URL: srv.URL, SendKey: mdmToDDM, RecvKey: []byte("wrong"), Auth: bearer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongRecv(ctx, &mdm.Request{}, ck, m); !errors.Is(err, proxywire.ErrBadSignature) {
		t.Fatalf("wrong receive key: %v", err)
	}
}
