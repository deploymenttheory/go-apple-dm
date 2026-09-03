package discovery_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/enroll/discovery"
	"github.com/deploymenttheory/go-apple-dm/plist"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/schema/errors"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

var errBoom = errors.New("boom")

func do(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHandler(t *testing.T) {
	t.Parallel()
	table := map[discovery.ModelFamily]discovery.Server{
		discovery.ModelFamilyMac:           {Version: discovery.VersionADDE, BaseURL: "https://mdm.example.com/enroll/adde"},
		discovery.ModelFamilyRealityDevice: {Version: discovery.VersionADDE, BaseURL: "https://mdm.example.com/enroll/adde"},
		discovery.ModelFamilyIPhone:        {Version: discovery.VersionBYOD, BaseURL: "https://mdm.example.com/enroll/byod"},
		discovery.ModelFamilyIPad:          {Version: discovery.VersionBYOD, BaseURL: "https://mdm.example.com/enroll/byod"},
	}
	static := discovery.Handler(discovery.Config{Router: discovery.StaticRouter(table), Logger: quietLogger()})

	t.Run("GoldenBody", func(t *testing.T) {
		t.Parallel()
		rec := do(t, static, http.MethodGet, "/.well-known/com.apple.remotemanagement?model-family=iPhone&user-identifier=user01%40example.com", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		want := `{"Servers":[{"Version":"mdm-byod","BaseURL":"https://mdm.example.com/enroll/byod"}]}`
		if got := rec.Body.String(); got != want {
			t.Fatalf("body\n got %s\nwant %s", got, want)
		}
		for k, v := range map[string]string{
			"Content-Type":           "application/json",
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
			"Content-Length":         "84",
		} {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s = %q, want %q", k, got, v)
			}
		}
	})

	t.Run("ModelFamilyTable", func(t *testing.T) {
		t.Parallel()
		var seen discovery.Request
		h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(_ context.Context, req discovery.Request) ([]discovery.Server, error) {
			seen = req
			if !req.ModelFamily.Known() {
				return nil, discovery.Reject("unknown device", "family "+string(req.ModelFamily))
			}
			return []discovery.Server{{Version: discovery.VersionADDE, BaseURL: "https://mdm.example.com/x"}}, nil
		}})
		for _, f := range discovery.ModelFamilies {
			rec := do(t, h, http.MethodGet, "/w?model-family="+string(f)+"&user-identifier=a%40b.example", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d", f, rec.Code)
			}
			if seen.ModelFamily != f || seen.UserIdentifier != "a@b.example" || seen.RawQuery != "model-family="+string(f)+"&user-identifier=a%40b.example" {
				t.Fatalf("%s: request %+v", f, seen)
			}
		}
		// Exact match: case and substring variants are not the documented
		// value, but they are preserved for the router.
		for _, bad := range []string{"iphone", "Macintosh", "model-family=Mac", ""} {
			rec := do(t, h, http.MethodGet, "/w?model-family="+url.QueryEscape(bad), nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%q: status %d, want 403", bad, rec.Code)
			}
			if string(seen.ModelFamily) != bad {
				t.Fatalf("%q: preserved as %q", bad, seen.ModelFamily)
			}
		}
	})

	t.Run("Head", func(t *testing.T) {
		t.Parallel()
		rec := do(t, static, http.MethodHead, "/w?model-family=Mac", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD wrote a body: %s", rec.Body.String())
		}
		if got := rec.Header().Get("Content-Length"); got != "84" {
			t.Fatalf("Content-Length %q", got)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type %q", got)
		}
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		t.Parallel()
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			rec := do(t, static, m, "/w?model-family=Mac", nil)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s: status %d", m, rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("%s: Allow %q", m, got)
			}
		}
	})

	t.Run("RejectWellKnownFailedJSON", func(t *testing.T) {
		t.Parallel()
		h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, &discovery.Rejection{Description: "not in directory", Message: "Contact IT."}
		}})
		for _, accept := range []string{"", "application/json", "*/*", "application/json, application/xml;q=0.5", "text/html", "garbage;;"} {
			rec := do(t, h, http.MethodGet, "/w?model-family=Mac&user-identifier=x%40y", map[string]string{"Accept": accept})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Accept %q: status %d", accept, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Accept %q: Content-Type %q", accept, got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("nosniff missing")
			}
			var failed schemaerrors.WellKnownFailed
			if err := json.Unmarshal(rec.Body.Bytes(), &failed, json.RejectUnknownMembers(true)); err != nil {
				t.Fatalf("Accept %q: body %s: %v", accept, rec.Body.String(), err)
			}
			if failed.Code != schemaerrors.ErrorCodeWellKnownFailed || *failed.Description != "not in directory" || *failed.Message != "Contact IT." {
				t.Fatalf("body %+v", failed)
			}
			if err := failed.Validate(support.Target{OS: support.IOS, Version: support.V(18, 0, 0)}); err != nil {
				t.Fatalf("schema: %v", err)
			}
		}
		want := `{"code":"com.apple.well-known.failed","description":"not in directory","message":"Contact IT."}`
		rec := do(t, h, http.MethodGet, "/w?model-family=Mac", nil)
		if rec.Body.String() != want {
			t.Fatalf("golden body %s", rec.Body.String())
		}
		// The sentinel and the helper both identify a rejection.
		rej := discovery.Reject("m", "d")
		if !errors.Is(rej, discovery.ErrReject) || !strings.Contains(rej.Error(), "d") {
			t.Fatalf("Reject: %v", rej)
		}
		wrapped := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, errors.Join(errBoom, discovery.Reject("m", "d"))
		}})
		if rec := do(t, wrapped, http.MethodGet, "/w", nil); rec.Code != http.StatusForbidden {
			t.Fatalf("wrapped rejection: status %d", rec.Code)
		}
	})

	t.Run("RejectWellKnownFailedPlist", func(t *testing.T) {
		t.Parallel()
		h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, discovery.Reject("Contact IT.", "not in directory")
		}})
		cases := map[string]string{
			"application/x-plist": "application/x-plist",
			"application/xml":     "application/xml; charset=utf-8",
			"text/xml":            "application/xml; charset=utf-8",
			"application/json;q=0.2, application/x-plist":                  "application/x-plist",
			"application/xml;q=0.9, application/json;q=.5":                 "application/xml; charset=utf-8",
			"text/html, application/x-plist;q=0.8, application/json;q=0.1": "application/x-plist",
		}
		for accept, wantCT := range cases {
			rec := do(t, h, http.MethodGet, "/w?model-family=Mac", map[string]string{"Accept": accept})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Accept %q: status %d", accept, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != wantCT {
				t.Fatalf("Accept %q: Content-Type %q, want %q", accept, got, wantCT)
			}
			var failed schemaerrors.WellKnownFailed
			if err := plist.Unmarshal(rec.Body.Bytes(), &failed); err != nil {
				t.Fatalf("Accept %q: %v", accept, err)
			}
			if failed.Code != schemaerrors.ErrorCodeWellKnownFailed || *failed.Description != "not in directory" || *failed.Message != "Contact IT." {
				t.Fatalf("body %+v", failed)
			}
		}
		// Equal preference stays JSON.
		rec := do(t, h, http.MethodGet, "/w?model-family=Mac", map[string]string{"Accept": "application/x-plist, application/json"})
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("tie: Content-Type %q", got)
		}
		// Optional fields are omitted when empty.
		bare := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, &discovery.Rejection{}
		}})
		rec = do(t, bare, http.MethodGet, "/w", map[string]string{"Accept": "application/x-plist"})
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "description") || strings.Contains(rec.Body.String(), "message") {
			t.Fatalf("bare rejection: %d %s", rec.Code, rec.Body.String())
		}
		// A code the schema does not allow is a server error, never served.
		badCode := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, &discovery.Rejection{Code: "com.example.other"}
		}})
		if rec := do(t, badCode, http.MethodGet, "/w", nil); rec.Code != http.StatusInternalServerError {
			t.Fatalf("bad code: status %d", rec.Code)
		}
	})

	t.Run("RedirectKeepsQuery", func(t *testing.T) {
		t.Parallel()
		h := discovery.Redirect("https://mdm.example.com/.well-known/com.apple.remotemanagement?tenant=acme&model-family=stale")
		rec := do(t, h, http.MethodGet, "/.well-known/com.apple.remotemanagement?model-family=Mac&user-identifier=user01%40example.com", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status %d", rec.Code)
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if loc.Scheme != "https" || loc.Host != "mdm.example.com" || loc.Path != "/.well-known/com.apple.remotemanagement" {
			t.Fatalf("location %s", loc)
		}
		q := loc.Query()
		if q.Get("model-family") != "Mac" || q.Get("user-identifier") != "user01@example.com" || q.Get("tenant") != "acme" {
			t.Fatalf("query %v", q)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control %q", got)
		}
		if rec := do(t, h, http.MethodHead, "/w?model-family=iPad", nil); rec.Code != http.StatusFound {
			t.Fatalf("HEAD status %d", rec.Code)
		}
		if rec := do(t, h, http.MethodPost, "/w", nil); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST status %d", rec.Code)
		}
		for _, bad := range []string{"http://mdm.example.com/w", "/relative", "://bad", ""} {
			if rec := do(t, discovery.Redirect(bad), http.MethodGet, "/w?model-family=Mac", nil); rec.Code != http.StatusInternalServerError {
				t.Fatalf("target %q: status %d", bad, rec.Code)
			}
		}
	})

	t.Run("HTTPSBaseURLOnly", func(t *testing.T) {
		t.Parallel()
		for name, servers := range map[string][]discovery.Server{
			"http":        {{Version: discovery.VersionBYOD, BaseURL: "http://mdm.example.com/enroll"}},
			"relative":    {{Version: discovery.VersionBYOD, BaseURL: "/enroll"}},
			"unparseable": {{Version: discovery.VersionBYOD, BaseURL: "https://exa mple.com/%zz"}},
			"noVersion":   {{BaseURL: "https://mdm.example.com/enroll"}},
			"empty":       {},
			"nil":         nil,
			"secondBad":   {{Version: discovery.VersionBYOD, BaseURL: "https://mdm.example.com/a"}, {Version: "mdm-next", BaseURL: "http://mdm.example.com/b"}},
		} {
			h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
				return servers, nil
			}})
			rec := do(t, h, http.MethodGet, "/w?model-family=Mac", nil)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("%s: status %d body %s", name, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "example.com") {
				t.Fatalf("%s: leaked detail %s", name, rec.Body.String())
			}
		}
		// Several valid servers are served in order.
		h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return []discovery.Server{{Version: "mdm-next", BaseURL: "https://mdm.example.com/v2"}, {Version: discovery.VersionADDE, BaseURL: "https://mdm.example.com/v1"}}, nil
		}})
		rec := do(t, h, http.MethodGet, "/w?model-family=Mac", nil)
		if want := `{"Servers":[{"Version":"mdm-next","BaseURL":"https://mdm.example.com/v2"},{"Version":"mdm-adde","BaseURL":"https://mdm.example.com/v1"}]}`; rec.Body.String() != want {
			t.Fatalf("body %s", rec.Body.String())
		}
	})

	t.Run("RouterErrorIs500", func(t *testing.T) {
		t.Parallel()
		h := discovery.Handler(discovery.Config{Logger: quietLogger(), Router: func(context.Context, discovery.Request) ([]discovery.Server, error) {
			return nil, errBoom
		}})
		rec := do(t, h, http.MethodGet, "/w?model-family=Mac", nil)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		none := discovery.Handler(discovery.Config{Logger: quietLogger()})
		if rec := do(t, none, http.MethodGet, "/w", nil); rec.Code != http.StatusInternalServerError {
			t.Fatalf("no router: status %d", rec.Code)
		}
		defaultLogger := discovery.Handler(discovery.Config{Router: discovery.StaticRouter(table)})
		if rec := do(t, defaultLogger, http.MethodGet, "/w?model-family=Watch", nil); rec.Code != http.StatusForbidden {
			t.Fatalf("static unknown family: status %d", rec.Code)
		}
	})
}

func TestParseModelFamily(t *testing.T) {
	t.Parallel()
	for _, f := range discovery.ModelFamilies {
		got, ok := discovery.ParseModelFamily(string(f))
		if !ok || got != f || !got.Known() {
			t.Fatalf("%s: %q %v", f, got, ok)
		}
	}
	got, ok := discovery.ParseModelFamily("Vision")
	if ok || got != "Vision" || got.Known() {
		t.Fatalf("unknown: %q %v", got, ok)
	}
	if len(discovery.ModelFamilies) != 6 {
		t.Fatalf("families %v", discovery.ModelFamilies)
	}
}
