package adminclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

// TestInventoryDownloadDoesNotTruncate checks exports can exceed the buffered response limit.
func TestInventoryDownloadDoesNotTruncate(t *testing.T) {
	count := int64(adminclient.MaxBody + 100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing credential")
		}
		_, _ = io.CopyN(w, zeroStream{}, count)
	}))
	defer server.Close()
	client, e := adminclient.New(adminclient.Config{BaseURL: server.URL, Token: "test"})
	if e != nil {
		t.Fatal(e)
	}
	sink := &countWriter{}
	if e := client.Download(t.Context(), "/inventory/exports", nil, sink); e != nil {
		t.Fatal(e)
	}
	if sink.n != count {
		t.Fatal("truncated export", sink.n, count)
	}
}

// TestInventoryDownloadRejectsPartialResults checks a late export failure is visible to scripts.
func TestInventoryDownloadRejectsPartialResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Inventory-Error")
		_, _ = io.WriteString(w, "partial data")
		w.Header().Set("X-Inventory-Error", "export_failed")
	}))
	defer server.Close()
	client, e := adminclient.New(adminclient.Config{BaseURL: server.URL, Token: "test"})
	if e != nil {
		t.Fatal(e)
	}
	var out strings.Builder
	if e := client.Download(context.Background(), "/inventory/exports", nil, &out); !errors.Is(e, adminclient.ErrStatus) {
		t.Fatal("partial stream reported success", e)
	}
}

type zeroStream struct{}

// Read generates bytes without allocating the entire export fixture.
func (zeroStream) Read(b []byte) (int, error) { clear(b); return len(b), nil }

type countWriter struct{ n int64 }

// Write counts successfully downloaded bytes without retaining the fleet payload.
func (w *countWriter) Write(b []byte) (int, error) { w.n += int64(len(b)); return len(b), nil }

// downloadTransport supplies transport and body failures without a remote dependency.
type downloadTransport func(*http.Request) (*http.Response, error)

// RoundTrip dispatches a controlled HTTP response.
func (f downloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type downloadBrokenReader struct{}

// Read simulates an interrupted response body.
func (downloadBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestInventoryDownloadFailures checks request errors, server errors, interrupted bodies and tracing.
func TestInventoryDownloadFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		broken, transport bool
	}{
		{"forbidden", 403, false, false}, {"error body", 500, true, false}, {"stream body", 200, true, false}, {"transport", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			traced := ""
			client, err := adminclient.New(adminclient.Config{BaseURL: "https://inventory.invalid", Token: "secret", Trace: func(line string) { traced = line }, HTTPClient: &http.Client{Transport: downloadTransport(func(r *http.Request) (*http.Response, error) {
				if tc.transport {
					return nil, io.ErrUnexpectedEOF
				}
				var body io.Reader = strings.NewReader(`{"error":"denied"}`)
				if tc.broken {
					body = downloadBrokenReader{}
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(body), Request: r}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Download(t.Context(), "/inventory/exports", nil, io.Discard); err == nil {
				t.Fatal("download failure ignored")
			}
			if traced == "" || strings.Contains(traced, "secret") {
				t.Fatal("unsafe trace", traced)
			}
		})
	}
}
