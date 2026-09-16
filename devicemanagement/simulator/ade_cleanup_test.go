package simulator_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

type adeResponseBody struct {
	io.Reader
	closed bool
}

func (b *adeResponseBody) Close() error {
	b.closed = true
	return nil
}

type adeTransport struct{ body *adeResponseBody }

func (r adeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: r.body, Header: make(http.Header), Request: req}, nil
}

func TestADEWebViewFailureClosesInitialResponse(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("ADE fixture")
	if err != nil {
		t.Fatal(err)
	}
	body := &adeResponseBody{Reader: strings.NewReader("login page")}
	device := simulator.New("device", simulator.WithClient(&http.Client{Transport: adeTransport{body}}),
		simulator.WithIdentity(&simulator.Identity{Cert: ca.Cert, Key: ca.Key}))
	failure := errors.New("web view cancelled")
	err = device.ADEEnroll(t.Context(), "https://mdm.example/enroll", simulator.ADEOptions{
		WebView: func(_ context.Context, _ *http.Response) (*http.Response, error) {
			return nil, failure
		},
	})
	if !errors.Is(err, failure) || !body.closed {
		t.Fatalf("error = %v, response closed = %v", err, body.closed)
	}
}
