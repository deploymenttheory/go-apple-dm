package proxyclient_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyserver"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

type correlationBackend struct {
	received chan string
}

func (b *correlationBackend) Handle(ctx context.Context, _ mdm.EnrollmentID, _ string, _ []byte) (ddm.Response, error) {
	b.received <- webhook.CorrelationID(ctx)
	return ddm.Response{Status: http.StatusOK, Body: []byte(`{}`)}, nil
}

func TestCorrelationAcrossAuthenticatedProxy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		correlation string
		tamper      bool
	}{
		{name: "forwarded", correlation: "corr_device_exchange"},
		{name: "absent"},
		{name: "changed after signing", correlation: "corr_device_exchange", tamper: true},
		{name: "injected after signing", tamper: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := &correlationBackend{received: make(chan string, 1)}
			ingress, err := proxyserver.Handler(proxyserver.Config{
				Backend: backend, ReplayStore: state.NewMemory(), RecvKey: sendKey, SendKey: recvKey,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			// Alter the HTTP request after the client signs it, at the peer's
			// ingress. Both changing and injecting correlation must be rejected.
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("correlation_id"); got != tc.correlation {
					t.Errorf("forwarded correlation = %q, want %q", got, tc.correlation)
				}
				if tc.tamper {
					query := r.URL.Query()
					query.Set("correlation_id", "corr_tampered")
					r.URL.RawQuery = query.Encode()
					r.RequestURI = r.URL.RequestURI()
				}
				ingress.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)
			egress := mustHandler(t, proxyclient.Config{URL: srv.URL, Client: srv.Client()})
			ck, message := dmCheckin(t, "D1", "tokens", nil)
			ctx := webhook.WithCorrelation(t.Context(), tc.correlation)
			response, err := egress(ctx, &mdm.Request{}, ck, message)
			if tc.tamper {
				if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, proxyclient.ErrUpstream) {
					t.Fatalf("modified correlation accepted: response=%+v err=%v", response, err)
				}
				select {
				case got := <-backend.received:
					t.Fatalf("unauthenticated correlation reached the backend: %q", got)
				default:
				}
				return
			}
			if err != nil || response.Status != http.StatusOK || string(response.Body) != `{}` {
				t.Fatalf("signed exchange failed: response=%+v err=%v", response, err)
			}
			select {
			case got := <-backend.received:
				if got != tc.correlation {
					t.Fatalf("backend correlation = %q, want %q", got, tc.correlation)
				}
			default:
				t.Fatal("request did not reach the backend")
			}
		})
	}
}
