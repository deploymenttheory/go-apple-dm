package contentcache_test

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
)

const minimal = `{"version":1,"reportDate":"2026-09-12T12:00:00Z","hostname":"cache.example","hardware":"Mac16,1","serverGUID":"13D4D110-B2B7-4F26-8E25-CD22E58C00EE"}`

type schema struct {
	Type       string            `json:"type"`
	Format     string            `json:"format"`
	Enum       []any             `json:"enum"`
	Properties map[string]schema `json:"properties"`
	Required   []string          `json:"required"`
	Items      *schema           `json:"items"`
}

func contract(t *testing.T) schema {
	t.Helper()
	data, err := os.ReadFile("testdata/metrics_report.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]schema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Schemas["CacheServerMetricsReport"]
}

func sample(s schema) any {
	if len(s.Enum) > 0 {
		return s.Enum[0]
	}
	switch s.Type {
	case "object":
		m := map[string]any{}
		for k, v := range s.Properties {
			m[k] = sample(v)
		}
		return m
	case "array":
		return []any{sample(*s.Items)}
	case "integer":
		return int64(0)
	case "number":
		return 0.25
	case "boolean":
		return false
	default:
		switch s.Format {
		case "date-time":
			return "2026-09-12T12:00:00.123456789+01:00"
		case "uuid":
			return "13D4D110-B2B7-4F26-8E25-CD22E58C00EE"
		default:
			return ""
		}
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReportMatchesAppleContract(t *testing.T) {
	t.Parallel()
	s := contract(t)
	for name, pair := range map[string]struct {
		source schema
		typ    reflect.Type
	}{
		"report": {s, reflect.TypeFor[contentcache.Report]()},
		"parent": {*s.Properties["parents"].Items, reflect.TypeFor[contentcache.Parent]()},
		"peer":   {*s.Properties["peers"].Items, reflect.TypeFor[contentcache.Peer]()},
	} {
		fields := map[string]bool{}
		for i := range pair.typ.NumField() {
			key, _, _ := strings.Cut(pair.typ.Field(i).Tag.Get("json"), ",")
			if key != "" {
				fields[key] = true
			}
		}
		if len(fields) != len(pair.source.Properties) {
			t.Fatalf("%s property count %d, want %d", name, len(fields), len(pair.source.Properties))
		}
		for key := range pair.source.Properties {
			if !fields[key] {
				t.Errorf("%s missing %s", name, key)
			}
		}
	}
	full := sample(s).(map[string]any)
	full["future"] = map[string]any{"value": nil}
	full["parents"].([]any)[0].(map[string]any)["future"] = true
	r, err := contentcache.Decode(encode(t, full))
	if err != nil {
		t.Fatal(err)
	}
	if r.Active == nil || *r.Active || r.ConnectedClients == nil || *r.ConnectedClients != 0 {
		t.Fatal("lost explicit zero/false")
	}
	got, want := jsontext.Value(encode(t, r)), jsontext.Value(encode(t, full))
	if err := got.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if err := want.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip changed values:\n%s\n%s", encode(t, r), encode(t, full))
	}
	r, err = contentcache.Decode([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if r.Active != nil || r.Parents != nil {
		t.Fatal("missing values were populated")
	}
	for _, key := range s.Required {
		m := sample(s).(map[string]any)
		delete(m, key)
		if _, err := contentcache.Decode(encode(t, m)); !errors.Is(err, contentcache.ErrInvalidReport) {
			t.Fatalf("missing %s: %v", key, err)
		}
	}
	for key, prop := range s.Properties {
		m := sample(s).(map[string]any)
		m[key] = nil
		if _, err := contentcache.Decode(encode(t, m)); !errors.Is(err, contentcache.ErrInvalidReport) {
			t.Fatalf("null %s: %v", key, err)
		}
		if prop.Format != "" || len(prop.Enum) > 0 {
			m[key] = "invalid"
			if prop.Type == "integer" {
				m[key] = int64(99)
			}
			if _, err := contentcache.Decode(encode(t, m)); !errors.Is(err, contentcache.ErrInvalidReport) {
				t.Fatalf("invalid %s: %v", key, err)
			}
		}
	}
	for _, key := range []string{"parents", "peers"} {
		for _, bad := range []any{nil, map[string]any{"guid": "bad"}, map[string]any{"guid": nil}} {
			m := sample(s).(map[string]any)
			m[key] = []any{bad}
			if _, err := contentcache.Decode(encode(t, m)); !errors.Is(err, contentcache.ErrInvalidReport) {
				t.Fatalf("invalid %s entry: %v", key, err)
			}
		}
		m := sample(s).(map[string]any)
		m[key] = []any{map[string]any{}}
		if _, err := contentcache.Decode(encode(t, m)); err != nil {
			t.Fatal(err)
		}
	}
	var nilReport *contentcache.Report
	if !errors.Is(nilReport.Validate(), contentcache.ErrInvalidReport) {
		t.Fatal("nil report accepted")
	}
}

func TestDecodeRejectsMalformedReports(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"null", "[]", "{}", "{", minimal + minimal, strings.Replace(minimal, `"version":1`, `"version":1,"version":2`, 1), strings.Replace(minimal, `"version":1`, `"version":1.5`, 1), strings.Replace(minimal, `cache.example`, "\xff", 1)} {
		if _, err := contentcache.Decode([]byte(bad)); !errors.Is(err, contentcache.ErrInvalidReport) {
			t.Fatalf("accepted %q: %v", bad, err)
		}
	}
}

var errCallback = errors.New("private callback diagnostic")

func TestReceiver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, media, body            string
		limit                                int64
		authFail, sinkFail, cancel, readFail bool
		want                                 int
	}{
		{name: "post", method: "POST", media: "application/json", body: minimal, want: 202},
		{name: "put", method: "PUT", media: "application/json; charset=utf-8", body: minimal, want: 202},
		{name: "method", method: "GET", want: 405},
		{name: "authorization", method: "POST", authFail: true, want: 401},
		{name: "media", method: "POST", media: "text/plain", want: 415},
		{name: "malformed media", method: "POST", media: "application/json;", want: 400},
		{name: "invalid", method: "POST", media: "application/json", body: "{}", want: 400},
		{name: "large", method: "POST", media: "application/json", body: minimal, limit: 4, want: 413},
		{name: "exact limit", method: "POST", media: "application/json", body: minimal, limit: int64(len(minimal)), want: 202},
		{name: "sink", method: "POST", media: "application/json", body: minimal, sinkFail: true, want: 503},
		{name: "cancel", method: "POST", media: "application/json", body: minimal, cancel: true, want: 503},
		{name: "read", method: "POST", media: "application/json", readFail: true, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted := 0
			h, err := contentcache.NewReceiver(contentcache.Config{
				MaxBodyBytes: tc.limit,
				Authorize: func(context.Context, *http.Request) error {
					if tc.authFail {
						return errCallback
					}
					return nil
				},
				Accept: func(ctx context.Context, r *contentcache.Report) error {
					accepted++
					if ctx.Err() != nil {
						t.Fatal(ctx.Err())
					}
					if r.Hostname == nil {
						t.Fatal("unvalidated report")
					}
					if tc.sinkFail {
						return errCallback
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(tc.method, "https://cache.example/metrics", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			if tc.readFail {
				r.Body = io.NopCloser(brokenReader{})
			}
			if tc.cancel {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d want %d: %s", w.Code, tc.want, w.Body)
			}
			if strings.Contains(w.Body.String(), errCallback.Error()) {
				t.Fatal("exposed callback error")
			}
			if tc.want != 202 && !tc.sinkFail && accepted != 0 {
				t.Fatal("rejected report reached sink")
			}
			if tc.want == 202 && accepted != 1 {
				t.Fatal("report was not accepted once")
			}
			if tc.want == 405 && w.Header().Get("Allow") != "POST, PUT" {
				t.Fatal("missing Allow")
			}
		})
	}
	for _, cfg := range []contentcache.Config{{}, {Authorize: func(context.Context, *http.Request) error { return nil }}, {Authorize: func(context.Context, *http.Request) error { return nil }, Accept: func(context.Context, *contentcache.Report) error { return nil }, MaxBodyBytes: -1}} {
		if _, err := contentcache.NewReceiver(cfg); !errors.Is(err, contentcache.ErrConfig) {
			t.Fatalf("invalid config: %v", err)
		}
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errCallback }

func FuzzDecode(f *testing.F) {
	f.Add([]byte(minimal))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := contentcache.Decode(b)
		if err != nil {
			return
		}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, err := contentcache.Decode(encode(t, r)); err != nil {
			t.Fatal(err)
		}
	})
}
