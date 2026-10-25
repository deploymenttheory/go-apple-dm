package collection_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
)

type deadlineTransport func(*http.Request) (*http.Response, error)

// RoundTrip supplies a controlled HTTP response through the real AXM client.
func (f deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type deadlineBody struct {
	ctx    context.Context
	finish func()
	reader io.Reader
	closed bool
}

// Read completes the response only after the request context has expired.
func (b *deadlineBody) Read(p []byte) (int, error) {
	if b.finish != nil {
		b.finish()
		b.finish = nil
	}
	if b.ctx != nil {
		<-b.ctx.Done()
	}
	return b.reader.Read(p)
}

// Close records ownership of the response body.
func (b *deadlineBody) Close() error { b.closed = true; return nil }

// TestResponseCompletionCancellation retains the request's cancellation when a
// successful HTTP response finishes with EOF instead of a transport error.
func TestResponseCompletionCancellation(t *testing.T) {
	for _, mode := range []string{"deadline", "canceled", "active-empty", "active-malformed"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				var cancel context.CancelFunc
				var want error
				failure := "transport_or_decode"
				body := &deadlineBody{reader: strings.NewReader("")}
				switch mode {
				case "deadline":
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					body.ctx, want, failure = ctx, context.DeadlineExceeded, "timeout"
				case "canceled":
					ctx, cancel = context.WithCancel(ctx)
					body.ctx, body.finish, want, failure = ctx, cancel, context.Canceled, "canceled"
				case "active-empty":
					want = axm.ErrDecode
				case "active-malformed":
					body.reader, want = strings.NewReader(`{"data":`), axm.ErrDecode
				}
				if cancel != nil {
					defer cancel()
				}
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				baseURL := "https://fixture.example"
				client, err := axm.New(context.Background(), axm.Config{
					ClientID: "BUSINESSAPI.fixture", KeyID: "fixture-key", PrivateKey: key,
					BaseURL: baseURL, TokenURL: baseURL + "/token",
					HTTPClient: &http.Client{Transport: deadlineTransport(func(r *http.Request) (*http.Response, error) {
						var responseBody io.ReadCloser = body
						if r.URL.Path == "/token" {
							responseBody = io.NopCloser(strings.NewReader(`{"access_token":"fixture-token","token_type":"Bearer","expires_in":3600}`))
						} else {
							calls++
							if r.Context().Err() != nil {
								t.Fatal("request expired before response body read")
							}
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: responseBody, Request: r}, nil
					})},
				})
				if err != nil {
					t.Fatal(err)
				}
				sink := &memorySink{}
				before := collection.Checkpoint{}
				after, err := (collection.Collector{Client: client}).Collect(ctx, request(collection.Coverage), before, sink)
				if !errors.Is(err, want) || calls != 1 || len(sink.batches) != 1 || !body.closed {
					t.Fatalf("error=%v want=%v calls=%d batches=%+v closed=%v", err, want, calls, sink.batches, body.closed)
				}
				batch := sink.batches[0]
				if batch.Outcome.Failure != failure || batch.Outcome.Success || !errors.Is(batch.Outcome.Err, want) || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(batch.After, before) || len(batch.Resources) != 0 || len(batch.Document) != 0 || batch.EnumerationComplete {
					t.Fatalf("failed attempt changed checkpoint/evidence: %+v", batch)
				}
			})
		})
	}
}
