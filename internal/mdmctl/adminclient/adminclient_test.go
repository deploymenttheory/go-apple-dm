package adminclient_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/mdmctl/adminclient"
)

func newClient(t *testing.T, h http.Handler) (*adminclient.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := adminclient.New(adminclient.Config{BaseURL: srv.URL, Token: "tok", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestNew(t *testing.T) {
	for name, url := range map[string]string{
		"empty":     "",
		"spaces":    "   ",
		"no scheme": "example.com/admin",
		"ftp":       "ftp://example.com",
		"no host":   "http://",
	} {
		if _, err := adminclient.New(adminclient.Config{BaseURL: url}); !errors.Is(err, adminclient.ErrConfig) {
			t.Errorf("%s: err = %v, want ErrConfig", name, err)
		}
	}
	c, err := adminclient.New(adminclient.Config{BaseURL: "https://example.com/", Timeout: time.Second})
	if err != nil || c == nil {
		t.Fatalf("New: %v", err)
	}
}

func TestDo(t *testing.T) {
	ctx := context.Background()

	t.Run("SendsTheBearerAndPath", func(t *testing.T) {
		var gotAuth, gotPath, gotQuery, gotAccept string
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.RawQuery
			gotAccept = r.Header.Get("Accept")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		resp, err := c.Do(ctx, http.MethodGet, "/principals", url.Values{"cursor": {"c1"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if gotAuth != "Bearer tok" {
			t.Fatalf("auth = %q", gotAuth)
		}
		if gotPath != "/admin/v1/principals" {
			t.Fatalf("path = %q", gotPath)
		}
		if gotQuery != "cursor=c1" {
			t.Fatalf("query = %q", gotQuery)
		}
		if gotAccept != "application/json" {
			t.Fatalf("accept = %q", gotAccept)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("status = %d", resp.Status)
		}
	})

	// The body is the server's bytes: key order and formatting survive so a
	// consumer sees canonical JSON unchanged.
	t.Run("BodyIsVerbatim", func(t *testing.T) {
		const body = `{"z":1,"a":2,"nested":{"b":  3}}`
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		resp, err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != body {
			t.Fatalf("body = %q, want it byte for byte", resp.Body)
		}
	})

	t.Run("EncodesBodies", func(t *testing.T) {
		var got []byte
		var contentType string
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, _ = io.ReadAll(r.Body)
			contentType = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusNoContent)
		}))
		if _, err := c.Do(ctx, http.MethodPost, "/x", nil, map[string]string{"Name": "a"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), `"Name"`) || contentType != "application/json" {
			t.Fatalf("body = %q type = %q", got, contentType)
		}
		if _, err := c.Do(ctx, http.MethodPost, "/x", nil, "raw string"); err != nil {
			t.Fatal(err)
		}
		if string(got) != "raw string" {
			t.Fatalf("string body = %q", got)
		}
		if _, err := c.Do(ctx, http.MethodPost, "/x", nil, []byte("raw bytes")); err != nil {
			t.Fatal(err)
		}
		if string(got) != "raw bytes" {
			t.Fatalf("byte body = %q", got)
		}
	})

	// Each status maps to a distinguishable error so the CLI can exit with a
	// code that means something, and the server's own message survives.
	t.Run("StatusErrors", func(t *testing.T) {
		for _, tc := range []struct {
			status int
			want   error
		}{
			{http.StatusUnauthorized, adminclient.ErrUnauthorized},
			{http.StatusForbidden, adminclient.ErrForbidden},
			{http.StatusNotFound, adminclient.ErrNotFound},
			{http.StatusConflict, adminclient.ErrStatus},
			{http.StatusInternalServerError, adminclient.ErrStatus},
		} {
			c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"Error":"the server said so"}`))
			}))
			resp, err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%d: err = %v, want %v", tc.status, err, tc.want)
			}
			if !strings.Contains(err.Error(), "the server said so") {
				t.Fatalf("%d: the server's message was dropped: %v", tc.status, err)
			}
			// The response is still returned, so a caller can inspect it.
			if resp == nil || resp.Status != tc.status {
				t.Fatalf("%d: response not returned", tc.status)
			}
		}
	})

	t.Run("NonJSONErrorBody", func(t *testing.T) {
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(strings.Repeat("html ", 200)))
		}))
		_, err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
		if !errors.Is(err, adminclient.ErrStatus) {
			t.Fatalf("err = %v", err)
		}
		if len(err.Error()) > 400 {
			t.Fatalf("the excerpt was not trimmed: %d characters", len(err.Error()))
		}
	})

	// Following a redirect would replay the bearer token to a host the
	// operator never named.
	t.Run("RefusesRedirects", func(t *testing.T) {
		var reached bool
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))
		defer other.Close()
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL, http.StatusFound)
		}))
		resp, err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
		if err == nil && resp.Status != http.StatusFound {
			t.Fatalf("status = %d, want the redirect returned unfollowed", resp.Status)
		}
		if reached {
			t.Fatal("the client followed a redirect and sent the token elsewhere")
		}
	})

	t.Run("TransportError", func(t *testing.T) {
		c, srv := newClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close()
		if _, err := c.Do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
			t.Fatal("a dead server returned no error")
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer slow.Close()
		c, err := adminclient.New(adminclient.Config{BaseURL: slow.URL, Timeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
			t.Fatal("a hanging server returned no error")
		}
	})

	// The trace shows the request, never the credential.
	t.Run("TraceNeverCarriesTheToken", func(t *testing.T) {
		var lines []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c, err := adminclient.New(adminclient.Config{
			BaseURL: srv.URL, Token: "super-secret-token",
			Trace: func(s string) { lines = append(lines, s) },
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(ctx, http.MethodGet, "/x", nil, nil); err != nil {
			t.Fatal(err)
		}
		if len(lines) == 0 {
			t.Fatal("nothing traced")
		}
		for _, l := range lines {
			if strings.Contains(l, "super-secret-token") {
				t.Fatalf("the trace carries the token: %q", l)
			}
		}
	})
}

// None of the reference admin CLIs paginate, so a large fleet silently
// truncates for them.
func TestEach(t *testing.T) {
	ctx := context.Background()

	t.Run("FollowsCursors", func(t *testing.T) {
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("cursor") {
			case "":
				_, _ = w.Write([]byte(`{"Items":[{"n":1},{"n":2}],"NextCursor":"c1"}`))
			case "c1":
				_, _ = w.Write([]byte(`{"Items":[{"n":3}],"NextCursor":"c2"}`))
			default:
				_, _ = w.Write([]byte(`{"Items":[{"n":4}]}`))
			}
		}))
		var got []string
		if err := c.Each(ctx, "/things", nil, func(v jsontext.Value) error {
			got = append(got, string(v))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 {
			t.Fatalf("items = %v, want four across three pages", got)
		}
	})

	// A server that repeated a cursor would spin forever.
	t.Run("RepeatedCursorIsAnError", func(t *testing.T) {
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"Items":[{"n":1}],"NextCursor":"same"}`))
		}))
		err := c.Each(ctx, "/things", nil, func(jsontext.Value) error { return nil })
		if !errors.Is(err, adminclient.ErrStatus) {
			t.Fatalf("err = %v, want ErrStatus", err)
		}
	})

	t.Run("CallbackErrorStops", func(t *testing.T) {
		sentinel := errors.New("stop")
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"Items":[{"n":1},{"n":2}],"NextCursor":"c1"}`))
		}))
		err := c.Each(ctx, "/things", nil, func(jsontext.Value) error { return sentinel })
		if !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want the callback's error", err)
		}
	})

	t.Run("BadJSON", func(t *testing.T) {
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		}))
		if err := c.Each(ctx, "/things", nil, func(jsontext.Value) error { return nil }); err == nil {
			t.Fatal("malformed page decoded")
		}
		if _, _, err := c.Page(ctx, "/things", nil); err == nil {
			t.Fatal("malformed page decoded by Page")
		}
	})

	t.Run("ServerError", func(t *testing.T) {
		c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		if err := c.Each(ctx, "/things", nil, func(jsontext.Value) error { return nil }); !errors.Is(err, adminclient.ErrForbidden) {
			t.Fatalf("err = %v", err)
		}
		if _, _, err := c.Page(ctx, "/things", nil); !errors.Is(err, adminclient.ErrForbidden) {
			t.Fatalf("Page err = %v", err)
		}
	})
}

func TestPage(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Items":[{"n":1}],"NextCursor":"next"}`))
	}))
	items, cursor, err := c.Page(context.Background(), "/things", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || cursor != "next" {
		t.Fatalf("items = %v cursor = %q", items, cursor)
	}
}

func TestServerConfig(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/v1/config" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"Role":"ddm","Version":"devel","Families":["ddm","principals"]}`))
	}))
	got, err := c.ServerConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "ddm" || len(got.Families) != 2 {
		t.Fatalf("config = %+v", got)
	}

	bad, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	if _, err := bad.ServerConfig(context.Background()); err == nil {
		t.Fatal("malformed config decoded")
	}
}

// -insecure was declared on Config and never acted on, so an operator
// testing against a self-signed lab certificate got the verification failure
// they had already opted out of.
func TestInsecureSkipsVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	t.Run("RefusedWithoutIt", func(t *testing.T) {
		c, err := adminclient.New(adminclient.Config{BaseURL: srv.URL, Token: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(context.Background(), http.MethodGet, "/config", nil, nil); err == nil {
			t.Fatal("an untrusted certificate was accepted without -insecure")
		}
	})

	t.Run("AcceptedWithIt", func(t *testing.T) {
		c, err := adminclient.New(adminclient.Config{BaseURL: srv.URL, Token: "t", Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(context.Background(), http.MethodGet, "/config", nil, nil)
		if err != nil {
			t.Fatalf("-insecure did not skip verification: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("status = %d", resp.Status)
		}
	})

	// A caller that supplied its own client keeps its own transport: taking
	// it over would be surprising, and the tests that inject srv.Client()
	// rely on it.
	t.Run("DoesNotOverrideACallersClient", func(t *testing.T) {
		c, err := adminclient.New(adminclient.Config{
			BaseURL: srv.URL, Token: "t", Insecure: true, HTTPClient: srv.Client(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(context.Background(), http.MethodGet, "/config", nil, nil); err != nil {
			t.Fatal(err)
		}
	})
}
