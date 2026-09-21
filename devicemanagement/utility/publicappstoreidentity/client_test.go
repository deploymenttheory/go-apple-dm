package publicappstoreidentity

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls the injected HTTP round-trip function.
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const listing = `{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.app","trackName":"Example","artistName":"Example Inc","version":"2.0","trackViewUrl":"https://apps.apple.com/app/id123"}]}`

var testStore = Store{Country: "gb", Entity: MacSoftware}

// fixtureClient creates a client returning the supplied response body and status with a
// Retry-After header.
func fixtureClient(t *testing.T, body string, status int) *Client {
	t.Helper()
	return &Client{HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
}

// TestSearchAndLookup checks public App Store request parameters, deadlines, decoding, and listing
// conversion.
func TestSearchAndLookup(t *testing.T) {
	c := fixtureClient(t, listing, 200)
	base := c.HTTPClient.Transport
	c.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.URL.Host != "itunes.apple.com" || q.Get("country") != "GB" || q.Get("entity") != "macSoftware" || q.Get("media") != "software" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("request: %s %v", r.URL, r.Header)
		}
		if r.URL.Path == "/search" && (q.Get("term") != "A & B+©" || q.Get("limit") != "50") {
			t.Fatalf("search: %s", r.URL)
		}
		if r.URL.Path == "/lookup" && q.Get("id") != "123" {
			t.Fatalf("lookup: %s", r.URL)
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("request has no deadline")
		}
		return base.RoundTrip(r)
	})
	apps, err := c.Search(t.Context(), Query{Term: "A & B+©", Store: testStore})
	if err != nil || len(apps) != 1 {
		t.Fatalf("search: %v %v", apps, err)
	}
	a, err := c.Lookup(t.Context(), 123, testStore)
	if err != nil || !reflect.DeepEqual(a, apps[0]) || a.BundleID != "com.example.app" || a.Name != "Example" || a.Developer != "Example Inc" || a.Version != "2.0" || a.Store.Country != "GB" {
		t.Fatalf("listing: %+v %v", a, err)
	}
}

// TestDeveloperFilterAndPlatformMetadata checks developer filter and platform metadata.
func TestDeveloperFilterAndPlatformMetadata(t *testing.T) {
	c := fixtureClient(t, `{"resultCount":3,"results":[
		{"trackId":1,"bundleId":"com.example.phone","trackName":"Example","artistName":"Example Inc","kind":"software","features":["iosUniversal"],"supportedDevices":["iPhone17,1","iPad16,3"]},
		{"trackId":2,"bundleId":"com.other.app","trackName":"Example","artistName":"Another Publisher"},
		{"trackId":3,"bundleId":"com.example.mac","trackName":"Example","artistName":"Example Inc","kind":"mac-software"}
	]}`, 200)
	base := c.HTTPClient.Transport
	calls := 0
	c.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Has("developer") || r.URL.Query().Get("limit") != "3" {
			t.Fatalf("local filter changed API query: %s", r.URL)
		}
		return base.RoundTrip(r)
	})
	for _, tc := range []struct {
		developer string
		ids       []int64
	}{
		{" eXAmple ", []int64{1, 3}},
		{"PUBLISHER", []int64{2}},
		{"missing", []int64{}},
		{"   ", []int64{1, 2, 3}},
	} {
		apps, err := c.Search(t.Context(), Query{Term: "Example", Developer: tc.developer, Store: testStore, Limit: 3})
		if err != nil || len(apps) != len(tc.ids) || apps == nil {
			t.Fatalf("%q: %+v %v", tc.developer, apps, err)
		}
		for i, app := range apps {
			if app.ID != tc.ids[i] {
				t.Fatalf("%q: unexpected order or match: %+v", tc.developer, apps)
			}
			if app.ID == 1 && (app.Kind != "software" || !reflect.DeepEqual(app.Features, []string{"iosUniversal"}) || !reflect.DeepEqual(app.SupportedDevices, []string{"iPhone17,1", "iPad16,3"})) {
				t.Fatalf("lost platform metadata: %+v", app)
			}
			if app.ID == 3 && (app.Kind != "mac-software" || app.Features != nil || app.SupportedDevices != nil) {
				t.Fatalf("invented platform metadata: %+v", app)
			}
		}
	}
	if calls != 4 {
		t.Fatalf("unexpected additional requests: %d", calls)
	}
	if _, err := fixtureClient(t, "failure", 503).Search(t.Context(), Query{Term: "Example", Store: testStore, Developer: "Example"}); !errors.Is(err, ErrStatus) {
		t.Fatalf("search failure: %v", err)
	}
}

// TestEntitiesAndDefaults checks App Store entity defaults and empty results.
func TestEntitiesAndDefaults(t *testing.T) {
	c := fixtureClient(t, `{"resultCount":0,"results":[]}`, 200)
	original := http.DefaultClient
	http.DefaultClient = c.HTTPClient
	t.Cleanup(func() { http.DefaultClient = original })
	c.HTTPClient = nil
	c.BaseURL = "https://example.test/catalog/"
	c.MaxBytes = 1024
	c.Timeout = time.Second
	for _, entity := range []Entity{Software, IPadSoftware, MacSoftware} {
		apps, err := c.Search(t.Context(), Query{Term: "missing", Store: Store{Country: "US", Entity: entity}, Limit: 200})
		if err != nil || apps == nil || len(apps) != 0 {
			t.Fatalf("empty result: %v %v", apps, err)
		}
	}
	if _, err := c.Lookup(t.Context(), 123, testStore); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// TestInvalidQueries checks invalid App Store queries and client configuration before requests are
// sent.
func TestInvalidQueries(t *testing.T) {
	for _, q := range []Query{
		{Store: testStore},
		{Term: "  ", Store: testStore},
		{Term: "app", Store: testStore, Limit: -1},
		{Term: "app", Store: testStore, Limit: 201},
		{Term: "app"},
		{Term: "app", Store: Store{Country: "USA", Entity: Software}},
		{Term: "app", Store: Store{Country: "1A", Entity: Software}},
		{Term: "app", Store: Store{Country: "A1", Entity: Software}},
		{Term: "app", Store: Store{Country: "US"}},
	} {
		if _, err := (&Client{}).Search(t.Context(), q); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", q, err)
		}
	}
	if _, err := (&Client{}).Lookup(t.Context(), 0, testStore); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, c := range []Client{
		{MaxBytes: -1}, {MaxBytes: math.MaxInt64}, {Timeout: -1}, {BaseURL: ":bad"}, {BaseURL: "ftp://example.test"}, {BaseURL: "https:///"}, {BaseURL: "https://user@example.test"}, {BaseURL: "https://example.test/?a=1"}, {BaseURL: "https://example.test/#fragment"},
	} {
		if _, err := c.Lookup(t.Context(), 123, testStore); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid client: %v", err)
		}
	}
}

// TestResponses checks App Store response validation and error classification.
func TestResponses(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		max    int64
		want   error
	}{
		{"bad", 200, 0, ErrDecode},
		{`{}`, 200, 0, ErrDecode},
		{`{"resultCount":1,"results":[]}`, 200, 0, ErrDecode},
		{`{"resultCount":1,"results":[{}]}`, 200, 0, ErrDecode},
		{`{"resultCount":1,"results":[{"trackId":124,"trackName":"A","bundleId":"com.example"}]}`, 200, 0, ErrDecode},
		{listing, 200, 10, ErrTooLarge},
		{"error", 429, 0, ErrStatus},
		{listing + "{}", 200, 0, ErrDecode},
	} {
		c := fixtureClient(t, tc.body, tc.status)
		c.MaxBytes = tc.max
		_, err := c.Lookup(t.Context(), 123, testStore)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v want %v", tc.body, err, tc.want)
		}
		if tc.status == 429 {
			var status *StatusError
			if !errors.As(err, &status) || status.StatusCode != 429 || status.RetryAfter != "60" || !strings.Contains(status.Error(), "429") {
				t.Fatal(err)
			}
		}
	}
}

type failingBody struct{ closed bool }

// Read returns io.ErrUnexpectedEOF without reading bytes.
func (*failingBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// Close records that the response body was closed.
func (b *failingBody) Close() error { b.closed = true; return nil }

// TestRequestFailures checks request failure propagation and closure of failed response bodies.
func TestRequestFailures(t *testing.T) {
	body := &failingBody{}
	c := &Client{HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Request: r}, nil
	})}}
	if _, err := c.Lookup(t.Context(), 123, testStore); !errors.Is(err, ErrRequest) || !errors.Is(err, io.ErrUnexpectedEOF) || !body.closed {
		t.Fatalf("read/close: %v closed=%v", err, body.closed)
	}
	c.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Lookup(ctx, 123, testStore); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	c.Timeout = time.Millisecond
	if _, err := c.Lookup(t.Context(), 123, testStore); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
