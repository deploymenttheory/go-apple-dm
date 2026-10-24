package collection_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
)

// fixture uses independent wire JSON and a real TLS/authentication boundary.
func fixture(t *testing.T, handler http.HandlerFunc) *axm.Client {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			if _, err := w.Write([]byte(`{"access_token":"fixture-token","token_type":"Bearer","expires_in":3600}`)); err != nil {
				t.Error(err)
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing bearer")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := axm.New(context.Background(), axm.Config{ClientID: "BUSINESSAPI.fixture", KeyID: "fixture-key", PrivateKey: key, BaseURL: server.URL, TokenURL: server.URL + "/token", HTTPClient: server.Client(), Retry: axm.Retry{Base: time.Nanosecond}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// write sends fixture JSON and reports write failures.
func write(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Error(err)
	}
}

// request constructs a stream with an opaque resource identifier independent of serial.
func request(op collection.Operation) collection.Request {
	r := collection.Request{Stream: collection.Stream{AccountID: "organization-A", Generation: "run-1", Operation: op}, Limit: 1}
	if op != collection.Devices {
		r.ResourceID = "opaque-id-unlike-serial"
	}
	return r
}

// memorySink deliberately owns no inventory model. It emulates a transactional
// compare-and-swap, retains request attempts separately and deduplicates replays.
type memorySink struct {
	checkpoint collection.Checkpoint
	resources  []collection.Resource
	batches    []collection.Batch
}

// Commit atomically updates caller-owned state and ignores already committed replays.
func (s *memorySink) Commit(_ context.Context, b collection.Batch) error {
	if reflect.DeepEqual(s.checkpoint, b.After) && b.Outcome.Success {
		return nil
	}
	if !reflect.DeepEqual(s.checkpoint, b.Before) {
		return errors.New("stale checkpoint")
	}
	s.resources = append(s.resources, b.Resources...)
	s.batches = append(s.batches, b)
	s.checkpoint = b.After
	return nil
}

// TestDevicesRestartAndFidelity preserves exact resources and document extensions across a fresh collector restart.
func TestDevicesRestartAndFidelity(t *testing.T) {
	const resource = `{"type":"orgDevices","id":"opaque-1","attributes":{"serialNumber":"SERIAL-ONE","future":9007199254740993,"nullValue":null,"zero":0,"enabled":false,"empty":[]},"relationships":{"futureRelation":{"data":{"type":"newResource","id":"related-id"},"future":true}},"futureTop":{"nested":null}}`
	const first = `{"data":[` + resource + `],"links":{"next":"/v1/orgDevices?cursor=second"},"included":[{"type":"newResource","id":"related-id","relationships":{"new":null},"future":9007199254740993}],"futureEnvelope":false}`
	calls := []string{}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("cursor"))
		if r.URL.Query().Get("cursor") == "second" {
			write(t, w, `{"data":[],"links":{}}`)
			return
		}
		write(t, w, first)
	})
	sink := &memorySink{}
	cp, err := (collection.Collector{Client: c}).Step(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Complete || sink.batches[0].EnumerationComplete {
		t.Fatal("partial enumeration established completion")
	}
	if string(sink.resources[0].Raw) != resource || string(sink.batches[0].Document) != first {
		t.Fatalf("source JSON changed: %+v", sink.batches[0])
	}
	encoded, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	var restored collection.Checkpoint
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	// New collector, deserialized checkpoint, same caller-owned sink.
	cp, err = (collection.Collector{Client: c}).Collect(context.Background(), request(collection.Devices), restored, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Complete || !sink.batches[1].EnumerationComplete || cp.Pages != 2 {
		t.Fatalf("no final completion: %+v", cp)
	}
	if !reflect.DeepEqual(calls, []string{"", "second"}) {
		t.Fatal(calls)
	}
	if _, err = (collection.Collector{Client: c}).Step(context.Background(), request(collection.Devices), cp, sink); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || len(sink.batches) != 2 {
		t.Fatal("completed stream performed work")
	}
}

// TestFailedCommitReplayAndCheckpointOwnership verifies rollback, slice ownership and uncertain post-commit recovery.
func TestFailedCommitReplayAndCheckpointOwnership(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cursor") {
		case "third":
			write(t, w, `{"data":[]}`)
		case "second":
			write(t, w, `{"data":[{"type":"orgDevices","id":"two"}],"links":{"next":"?cursor=third"}}`)
		default:
			write(t, w, `{"data":[{"type":"orgDevices","id":"one"}],"links":{"next":"?cursor=second"}}`)
		}
	})
	sink := &memorySink{}
	collector := collection.Collector{Client: c}
	cp, err := collector.Step(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	// Leave spare capacity to catch accidental mutation of caller-owned slices.
	cp.Seen = append(make([]string, 0, 8), cp.Seen...)
	original, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("rollback")
	returned, err := collector.Step(context.Background(), request(collection.Devices), cp, collection.SinkFunc(func(_ context.Context, b collection.Batch) error {
		if b.After.Pages != 2 || len(b.Resources) != 1 {
			t.Fatal(b)
		}
		return failed
	}))
	if !errors.Is(err, failed) || !reflect.DeepEqual(returned, cp) {
		t.Fatalf("advance on failed commit: %+v %v", returned, err)
	}
	encoded, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(encoded) || len(sink.resources) != 1 {
		t.Fatal("failed commit changed input/evidence")
	}
	cp, err = (collection.Collector{Client: c}).Step(context.Background(), request(collection.Devices), returned, sink)
	if err != nil || cp.Pages != 2 || len(sink.resources) != 2 {
		t.Fatalf("replay failed: %+v %v", cp, err)
	}
	// Simulate ambiguous post-commit failure: the caller reloads sink.checkpoint,
	// rather than assuming the returned checkpoint represents committed state.
	_, err = collector.Step(context.Background(), request(collection.Devices), cp, collection.SinkFunc(func(ctx context.Context, b collection.Batch) error {
		if err := sink.Commit(ctx, b); err != nil {
			return err
		}
		return failed
	}))
	if !errors.Is(err, failed) {
		t.Fatal("expected ambiguous commit error", err)
	}
	if !sink.checkpoint.Complete {
		t.Fatal("sink did not commit final checkpoint")
	}
	cp, err = collector.Collect(context.Background(), request(collection.Devices), sink.checkpoint, sink)
	if err != nil || !cp.Complete || len(sink.resources) != 2 {
		t.Fatal("reload replayed committed final page", err)
	}
}

// TestDetailAssignmentAndCoverage retains singular resources, explicit null assignment and every coverage page.
func TestDetailAssignmentAndCoverage(t *testing.T) {
	cases := []struct {
		op   collection.Operation
		body string
	}{
		{collection.Detail, `{"data":{"type":"orgDevices","id":"opaque-id-unlike-serial","attributes":{"serialNumber":"SERIAL"},"relationships":{"assignedServer":{"data":null}}}}`},
		{collection.Assignment, `{"data":{"type":"mdmServers","id":"server-id","attributes":{"future":false},"relationships":{"future":{"data":[]}}}}`},
		{collection.Assignment, `{"data":null}`},
	}
	for i, test := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "opaque-id-unlike-serial") {
					t.Error(r.URL.Path)
				}
				write(t, w, test.body)
			})
			sink := &memorySink{}
			cp, err := (collection.Collector{Client: c}).Collect(context.Background(), request(test.op), collection.Checkpoint{}, sink)
			if err != nil {
				t.Fatal(err)
			}
			var expected struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(test.body), &expected); err != nil {
				t.Fatal(err)
			}
			if !cp.Complete || string(sink.resources[0].Raw) != string(expected.Data) || sink.batches[0].EnumerationComplete {
				t.Fatalf("single resource changed %+v", sink)
			}
		})
	}
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/opaque-id-unlike-serial/appleCareCoverage") {
			t.Error(r.URL.Path)
		}
		if r.URL.Query().Get("cursor") == "next" {
			write(t, w, `{"data":[{"type":"appleCareCoverage","id":"plan-2","attributes":{"future":null}}]}`)
			return
		}
		write(t, w, `{"data":[{"type":"appleCareCoverage","id":"plan-1","attributes":{"future":false}}],"meta":{"paging":{"nextCursor":"next"}}}`)
	})
	sink := &memorySink{}
	cp, err := (collection.Collector{Client: c}).Collect(context.Background(), request(collection.Coverage), collection.Checkpoint{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(sink.resources) != 2 || !cp.Complete || !sink.batches[1].EnumerationComplete {
		t.Fatal(sink)
	}
}

// TestFailuresNeverEstablishAbsence keeps permission, missing-resource and malformed-document outcomes distinct from emptiness.
func TestFailuresNeverEstablishAbsence(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				write(t, w, `{"errors":[{"status":"failed","detail":"PRIVATE-SECRET"}]}`)
			})
			sink := &memorySink{}
			cp, err := (collection.Collector{Client: c}).Collect(context.Background(), request(collection.Assignment), collection.Checkpoint{}, sink)
			if err == nil || cp.Complete || !reflect.DeepEqual(cp, collection.Checkpoint{}) {
				t.Fatalf("failure treated as success: %+v %v", cp, err)
			}
			if len(sink.batches) != 1 || len(sink.resources) != 0 {
				t.Fatal(sink)
			}
			outcome := sink.batches[0].Outcome
			if outcome.Success || outcome.HTTPStatus != status || sink.batches[0].EnumerationComplete || sink.batches[0].Stream != request(collection.Assignment).Stream {
				t.Fatal(outcome)
			}
			encoded, err := json.Marshal(outcome)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "PRIVATE-SECRET") {
				t.Fatal("outcome leaked response")
			}
		})
	}
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `broken`, `{"data":[{}]}`} {
		t.Run(body, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, _ *http.Request) { write(t, w, body) })
			sink := &memorySink{}
			cp, err := (collection.Collector{Client: c}).Collect(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink)
			if err == nil || cp.Complete || len(sink.resources) != 0 {
				t.Fatalf("malformed page completed: %+v %v", cp, err)
			}
		})
	}
}

// TestPartialFailureRetainsCheckpointAndEvidence resumes coverage after a failed second page without clearing evidence.
func TestPartialFailureRetainsCheckpointAndEvidence(t *testing.T) {
	secondFails := true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "two" {
			if secondFails {
				w.WriteHeader(http.StatusForbidden)
				write(t, w, `{"errors":[]}`)
				return
			}
			write(t, w, `{"data":[]}`)
			return
		}
		write(t, w, `{"data":[{"type":"appleCareCoverage","id":"plan-1"}],"links":{"next":"?cursor=two"}}`)
	})
	sink := &memorySink{}
	req := request(collection.Coverage)
	cp, err := (collection.Collector{Client: c}).Collect(context.Background(), req, collection.Checkpoint{}, sink)
	if err == nil || cp.Complete || cp.Pages != 1 || len(sink.resources) != 1 || sink.batches[1].Outcome.Success {
		t.Fatalf("partial enumeration lost: %+v %v", cp, err)
	}
	secondFails = false
	cp, err = (collection.Collector{Client: c}).Collect(context.Background(), req, sink.checkpoint, sink)
	if err != nil || !cp.Complete || len(sink.resources) != 1 {
		t.Fatalf("restart failed: %+v %v", cp, err)
	}
}

// TestCancellationAndTransport retains context errors and records transport failure separately from empty enumeration.
func TestCancellationAndTransport(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c := fixture(t, func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		// Keep the request in flight until the caller observes cancellation.
		// Returning here can race with net/http sending an implicit empty 200.
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-entered; cancel() }()
	sink := &memorySink{}
	_, err := (collection.Collector{Client: c}).Collect(ctx, request(collection.Devices), collection.Checkpoint{}, sink)
	if !errors.Is(err, context.Canceled) || len(sink.batches) != 1 || sink.batches[0].Outcome.Failure != "canceled" {
		t.Fatalf("cancel: %+v %v", sink, err)
	}
	calls := 0
	_, err = (collection.Collector{Client: c}).Step(ctx, request(collection.Devices), collection.Checkpoint{}, collection.SinkFunc(func(context.Context, collection.Batch) error { calls++; return nil }))
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	c = fixture(t, func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("HTTP1 fixture cannot hijack connection")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	sink = &memorySink{}
	_, err = (collection.Collector{Client: c}).Collect(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink)
	if !errors.Is(err, axm.ErrTransport) || len(sink.batches) != 1 || sink.batches[0].Outcome.Failure != "transport_or_decode" {
		t.Fatal("transport", err, sink)
	}
	c = fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected server call") })
	// Cancel while authenticating exercises a transport failure through a real
	// client; classification remains separate from successful empty enumeration.
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	_, err = (collection.Collector{Client: c}).Step(deadline, request(collection.Devices), collection.Checkpoint{}, sink)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

// TestValidationPaginationAndCompletionBounds rejects invalid configuration, incompatible checkpoints and cyclic or excessive pagination.
func TestValidationPaginationAndCompletionBounds(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		write(t, w, `{"data":[],"links":{"self":"/v1/orgDevices","next":"/v1/orgDevices"}}`)
	})
	sink := collection.SinkFunc(func(context.Context, collection.Batch) error { return nil })
	invalid := []collection.Request{request("unknown"), request(collection.Devices), request(collection.Detail), request(collection.Devices), request(collection.Devices)}
	invalid[1].AccountID = ""
	invalid[2].ResourceID = ""
	invalid[3].ResourceID = "SERIAL"
	invalid[4].Limit = 1001
	for _, req := range invalid {
		if _, err := (collection.Collector{Client: c}).Step(context.Background(), req, collection.Checkpoint{}, sink); !errors.Is(err, axm.ErrArgument) {
			t.Fatal(req, err)
		}
	}
	for _, collector := range []collection.Collector{{}, {Client: c, MaxPages: -1}} {
		if _, err := collector.Step(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink); !errors.Is(err, axm.ErrArgument) {
			t.Fatal(err)
		}
	}
	if _, err := (collection.Collector{Client: c}).Step(context.Background(), request(collection.Devices), collection.Checkpoint{}, nil); !errors.Is(err, axm.ErrArgument) {
		t.Fatal(err)
	}
	for _, cp := range []collection.Checkpoint{{Version: 2}, {Version: 1}, {Complete: true}, {Pages: 1}} {
		if _, err := (collection.Collector{Client: c}).Step(context.Background(), request(collection.Devices), cp, sink); !errors.Is(err, axm.ErrArgument) {
			t.Fatal(cp, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("validation made requests")
	}
	collector := collection.Collector{Client: c}
	cp, err := collector.Step(context.Background(), request(collection.Devices), collection.Checkpoint{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (collection.Collector{Client: c, MaxPages: 1}).Step(context.Background(), request(collection.Devices), cp, sink); !errors.Is(err, axm.ErrPageCap) {
		t.Fatal(err)
	}
	if _, err = collector.Step(context.Background(), request(collection.Devices), cp, sink); !errors.Is(err, axm.ErrNextLink) {
		t.Fatal(err)
	}
}

// TestDeadlineAndMalformedSingles covers request deadlines, malformed singular resources and nil sink adapters.
func TestDeadlineAndMalformedSingles(t *testing.T) {
	c := fixture(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	sink := &memorySink{}
	_, err := (collection.Collector{Client: c}).Collect(ctx, request(collection.Coverage), collection.Checkpoint{}, sink)
	if !errors.Is(err, context.DeadlineExceeded) || len(sink.batches) != 1 || sink.batches[0].Outcome.Failure != "timeout" {
		t.Fatal(err, sink)
	}
	for _, operation := range []collection.Operation{collection.Detail, collection.Assignment} {
		t.Run(string(operation), func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, _ *http.Request) { write(t, w, `{"data":{}}`) })
			sink := &memorySink{}
			_, err := (collection.Collector{Client: c}).Collect(context.Background(), request(operation), collection.Checkpoint{}, sink)
			if !errors.Is(err, axm.ErrDecode) || len(sink.resources) != 0 {
				t.Fatal(err, sink)
			}
		})
	}
	var nilSink collection.SinkFunc
	if err := nilSink.Commit(context.Background(), collection.Batch{}); !errors.Is(err, axm.ErrArgument) {
		t.Fatal(err)
	}
}
