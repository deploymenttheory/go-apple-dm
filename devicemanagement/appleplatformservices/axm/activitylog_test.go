package axm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type activityTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates a test exchange to its controlled transport.
func (f activityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestActivityDownloadOrigins uses independent TLS API/storage servers with a
// transport mapping only the documented storage hostname onto the latter.
func TestActivityDownloadOrigins(t *testing.T) {
	t.Parallel()
	const csv = "serialNumber,activityType,status,reason,extra\nSERIAL,ASSIGN_DEVICES,SUCCESS,,unknown\nOTHER,ASSIGN_DEVICES,FAILED,locked,retained\n"
	var storageCalls int
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageCalls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("storage received API credentials")
		}
		if r.URL.Query().Get("Signature") != "secret" {
			t.Error("signed query not preserved")
		}
		switch r.URL.Path {
		case "/expired":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("secret"))
		case "/redirect":
			http.Redirect(w, r, "https://foreign.example/secret", http.StatusFound)
		case "/malformed":
			_, _ = w.Write([]byte("a,b\n1,\"unterminated"))
		case "/partial":
			w.Header().Set("Content-Length", "999")
			_, _ = w.Write([]byte(csv))
		case "/empty":
			_, _ = w.Write([]byte("serialNumber,status\n"))
		default:
			_, _ = w.Write([]byte(csv))
		}
	}))
	t.Cleanup(storage.Close)
	api := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stub-token" {
			t.Error("API bearer missing")
		}
		_, _ = w.Write([]byte(csv))
	})
	var logs strings.Builder
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	storageURL, _ := url.Parse("https://store.blobstore.apple.com")
	jar.SetCookies(storageURL, []*http.Cookie{{Name: "api-secret", Value: "secret"}})
	c := stubClient(t, api, func(cfg *Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cfg.HTTPClient = &http.Client{Jar: jar, Transport: activityTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() != "store.blobstore.apple.com" {
				return api.Client().Transport.RoundTrip(r)
			}
			copy := r.Clone(r.Context())
			u := *r.URL
			target, _ := url.Parse(storage.URL)
			u.Scheme, u.Host = target.Scheme, target.Host
			copy.URL = &u
			return storage.Client().Transport.RoundTrip(copy)
		})}
	})
	body, err := c.FetchActivityLog(t.Context(), "https://store.blobstore.apple.com/log?Signature=secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != csv {
		t.Fatalf("CSV changed: %q", got)
	}
	if strings.Contains(logs.String(), "secret") {
		t.Fatal("signed URL logged")
	}
	for _, path := range []string{"expired", "redirect", "malformed", "partial", "empty"} {
		t.Run(path, func(t *testing.T) {
			reader, err := c.FetchActivityLog(t.Context(), "https://store.blobstore.apple.com/"+path+"?Signature=secret")
			if reader != nil || err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe success/error: %v %v", reader, err)
			}
			if path == "expired" && !hasStatus(err, http.StatusForbidden) {
				t.Fatal(err)
			}
		})
	}
	before := storageCalls
	for _, raw := range []string{"https://store.blobstore.apple.com.attacker.example/log?secret", "https://store.blobstore.apple.com:444/log?secret", "https://evil.example/log?secret", "https://user:secret@store.blobstore.apple.com/log", "http://store.blobstore.apple.com/secret", "https://store.blobstore.apple.com/#secret", "https://store.blobstore.apple.com/%zz?secret"} {
		r, err := c.FetchActivityLog(t.Context(), raw)
		if r != nil || err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe origin accepted/error: %v %v", r, err)
		}
	}
	if storageCalls != before {
		t.Fatal("rejected origin contacted storage")
	}
	apiBody, err := c.FetchActivityLog(t.Context(), api.URL+"/log")
	if err != nil {
		t.Fatal(err)
	}
	if err := apiBody.Close(); err != nil {
		t.Fatal(err)
	}
	c.cfg.ActivityLogMaxBytes = 8
	if r, err := c.FetchActivityLog(t.Context(), "https://store.blobstore.apple.com/log?Signature=secret"); r != nil || !errors.Is(err, ErrActivityLog) {
		t.Fatalf("limit: %v %v", r, err)
	}
}

// TestActivityDownloadFailures checks config validation, sanitized errors and malformed CSV.
func TestActivityDownloadFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if c, err := New(t.Context(), func() Config { cfg := f.cfg; cfg.ActivityLogMaxBytes = -1; return cfg }()); c != nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("negative limit: %v %v", c, err)
	}
	c := f.client(t, nil)
	c.http.Transport = activityTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport contains Signature=secret")
	})
	if r, err := c.FetchActivityLog(t.Context(), "https://store.blobstore.apple.com/log?Signature=secret"); r != nil || !errors.Is(err, ErrTransport) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("transport: %v %v", r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.FetchActivityLog(ctx, "https://store.blobstore.apple.com/log?Signature=secret"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, csv := range []string{"", "one\nrow\n", "a,a\n1,2\n", "a,\n1,2\n", "a,b\n1,2,3\n", "a,b\n"} {
		if err := validateActivityCSV([]byte(csv)); !errors.Is(err, ErrActivityLog) {
			t.Fatalf("accepted %q", csv)
		}
	}
}

// TestVerifyActivityLogOutcomes checks completeness separately from per-device success.
func TestVerifyActivityLogOutcomes(t *testing.T) {
	t.Parallel()
	columns := ActivityLogColumns{Serial: "Serial Number", Outcome: "Result", Success: "Done"}
	for _, tc := range []struct {
		name, body        string
		complete, success bool
	}{
		{"successful", "Serial Number,Result,Reason\nS1,Done,\nS2,Done,\n", true, true},
		{"per device failure", "Serial Number,Result,Reason\nS1,Done,\nS2,Failed,locked\n", true, false},
		{"partial", "Serial Number,Result,Reason\nS1,Done,\n", false, false},
		{"duplicate", "Serial Number,Result,Reason\nS1,Done,\nS1,Done,\n", false, false},
		{"unexpected", "Serial Number,Result,Reason\nS1,Done,\nOTHER,Done,\n", false, false},
		{"missing outcome", "Serial Number,Result,Reason\nS1,Done,\nS2,,locked\n", false, false},
		{"unknown headers", "serialNumber,status,reason\nS1,SUCCESS,\nS2,SUCCESS,\n", false, false},
		{"malformed", "Serial Number,Result,Reason\nS1,Done,\nS2,Done\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := VerifyActivityLog(strings.NewReader(tc.body), columns, []string{"S1", "S2"})
			if result.Complete != tc.complete || result.Succeeded != tc.success || (err != nil) == tc.complete {
				t.Fatalf("%+v %v", result, err)
			}
			if tc.name == "per device failure" && result.Rows[1]["Reason"] != "locked" {
				t.Fatal("failure detail lost")
			}
		})
	}
	for _, serials := range [][]string{nil, {""}, {"S", "S"}} {
		if _, err := VerifyActivityLog(strings.NewReader("a,b\n1,2\n"), columns, serials); !errors.Is(err, ErrArgument) {
			t.Fatal(err)
		}
	}
	if _, err := VerifyActivityLog(nil, columns, []string{"S"}); !errors.Is(err, ErrArgument) {
		t.Fatal(err)
	}
	if _, err := VerifyActivityLog(strings.NewReader("a,b\n1,2\n"), ActivityLogColumns{}, []string{"S"}); !errors.Is(err, ErrArgument) {
		t.Fatal(err)
	}
}

// ExampleVerifyActivityLog shows complete evidence with a retained device failure.
func ExampleVerifyActivityLog() {
	log, err := VerifyActivityLog(strings.NewReader("Serial,Outcome,Reason\nS1,Done,\nS2,Failed,locked\n"),
		ActivityLogColumns{Serial: "Serial", Outcome: "Outcome", Success: "Done"}, []string{"S1", "S2"})
	fmt.Println(log.Complete, log.Succeeded, log.Rows[1]["Reason"], err)
	// Output: true false locked <nil>
}

// TestActivityDownloadAPIReplay uses independent API responses to expire a cached
// token and verifies that storage authentication failures never trigger OAuth.
func TestActivityDownloadAPIReplay(t *testing.T) {
	t.Parallel()
	for _, alwaysUnauthorized := range []bool{false, true} {
		name := "expired cached token"
		if alwaysUnauthorized {
			name = "bounded second unauthorized"
		}
		t.Run(name, func(t *testing.T) {
			tokenCalls, downloadCalls := 0, 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/auth/oauth2/token":
					tokenCalls++
					_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","token_type":"Bearer","expires_in":3600,"scope":"business.api"}`, tokenCalls)
				case "/v1/orgDevices":
					_, _ = w.Write([]byte(`{"data":[]}`))
				case "/log":
					downloadCalls++
					if alwaysUnauthorized || r.Header.Get("Authorization") == "Bearer token-1" {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte("signed secret in error body"))
						return
					}
					if r.Header.Get("Authorization") != "Bearer token-2" {
						t.Error("refreshed bearer missing")
					}
					_, _ = w.Write([]byte("serial,status\nSERIAL,SUCCESS\n"))
				}
			}))
			t.Cleanup(srv.Close)
			c := stubClient(t, srv, nil)
			if _, err := c.ListOrgDevices(t.Context(), ListOptions{}); err != nil {
				t.Fatal(err)
			}
			body, err := c.FetchActivityLog(t.Context(), srv.URL+"/log?Signature=secret")
			if alwaysUnauthorized {
				var authErr *AuthError
				if body != nil || !errors.As(err, &authErr) || !IsUnauthorized(err) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unsafe auth result: %v %v", body, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := body.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if tokenCalls != 2 || downloadCalls != 2 {
				t.Fatalf("unbounded or missing replay: tokens=%d downloads=%d", tokenCalls, downloadCalls)
			}
		})
	}
	srv := stub(t, func(http.ResponseWriter, *http.Request) { t.Error("storage 401 attempted API access") })
	c := stubClient(t, srv, func(cfg *Config) {
		cfg.HTTPClient = &http.Client{Transport: activityTransport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "" {
				t.Error("storage received bearer")
			}
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("secret")), Header: make(http.Header), Request: r}, nil
		})}
	})
	if body, err := c.FetchActivityLog(t.Context(), "https://store.blobstore.apple.com/log?Signature=secret"); body != nil || !hasStatus(err, http.StatusUnauthorized) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("storage auth: %v %v", body, err)
	}
}
