package appsbooks_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// protocolClient supplies service discovery and ownership without a network.
// Individual cases control only the response or limit under test.
func protocolClient(t *testing.T, change func(*appsbooks.ServiceConfiguration), handler http.HandlerFunc) (*appsbooks.Client, appsbooks.Config) {
	t.Helper()
	service := appsbooks.ServiceConfiguration{
		Limits: map[string]int{
			"maxRequestPerSecond": 15, "maxAssets": 2, "maxClientUserIds": 2, "maxSerialNumbers": 2,
			"maxRevokeClientUserIds": 2, "maxRevokeSerialNumbers": 2, "maxUsers": 2,
			"maxMdmIdLength": 100, "maxMdmNameLength": 100, "maxMdmMetadataLength": 255, "maxNotificationLength": 512,
		},
		URLs:       map[string]string{},
		ErrorCodes: []appsbooks.Failure{{Number: 1, Info: []byte(`{"detail":"original"}`)}},
	}
	for key, path := range map[string]string{
		"clientConfig": "client/config", "getAssets": "assets", "getUsers": "users",
		"getAssignments": "assignments", "associateAssets": "assets/associate", "disassociateAssets": "assets/disassociate",
		"revokeAssets": "assets/revoke", "createUsers": "users/create", "updateUsers": "users/update", "retireUsers": "users/retire", "eventStatus": "status",
	} {
		service.URLs[key] = "https://example.com/" + path
	}
	service.URLs["invitationEmail"] = "https://example.com/invite?code=%25inviteCode%25"
	if change != nil {
		change(&service)
	}
	cfg := appsbooks.Config{
		SToken: base64.StdEncoding.EncodeToString([]byte(`{"token":"private-content-token","expDate":"2030-01-01T00:00:00Z"}`)),
		MDMID:  "ours", UID: "L", BaseURL: "https://example.com", ReadRetries: 2,
		Clock: &advancingClock{now: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)},
	}
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Path == "/service/config":
			if err := json.NewEncoder(w).Encode(service); err != nil {
				t.Error(err)
			}
		case r.URL.Path == "/client/config" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"uId":"L","mdmInfo":{"id":"ours"}}`)
		default:
			if handler == nil {
				t.Error("unexpected request", r.Method, r.URL.Path)
			} else {
				handler(w, r)
			}
		}
		return w.Result(), nil
	})}
	client, err := appsbooks.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client, cfg
}

// TestConfigValidation rejects invalid client configuration while allowing offline default
// construction.
func TestConfigValidation(t *testing.T) {
	_, base := protocolClient(t, nil, nil)
	for _, tc := range []struct {
		name   string
		change func(*appsbooks.Config)
		want   error
	}{
		{"missing MDM", func(c *appsbooks.Config) { c.MDMID = "" }, appsbooks.ErrConfig},
		{"negative retries", func(c *appsbooks.Config) { c.ReadRetries = -1 }, appsbooks.ErrConfig},
		{"too many retries", func(c *appsbooks.Config) { c.ReadRetries = 6 }, appsbooks.ErrConfig},
		{"oversized token", func(c *appsbooks.Config) { c.SToken = strings.Repeat("x", 1<<20+1) }, appsbooks.ErrConfig},
		{"invalid base64", func(c *appsbooks.Config) { c.SToken = "!" }, appsbooks.ErrConfig},
		{"invalid JSON", func(c *appsbooks.Config) { c.SToken = base64.StdEncoding.EncodeToString([]byte("{")) }, appsbooks.ErrConfig},
		{"missing token", func(c *appsbooks.Config) {
			c.SToken = base64.StdEncoding.EncodeToString([]byte(`{"expDate":"2030-01-01T00:00:00Z"}`))
		}, appsbooks.ErrConfig},
		{"invalid expiry", func(c *appsbooks.Config) {
			c.SToken = base64.StdEncoding.EncodeToString([]byte(`{"token":"secret","expDate":"tomorrow"}`))
		}, appsbooks.ErrConfig},
		{"expired", func(c *appsbooks.Config) {
			c.SToken = base64.StdEncoding.EncodeToString([]byte(`{"token":"secret","expDate":"2020-01-01T00:00:00Z"}`))
		}, appsbooks.ErrExpired},
		{"HTTP URL", func(c *appsbooks.Config) { c.BaseURL = "http://example.com" }, appsbooks.ErrConfig},
		{"URL query", func(c *appsbooks.Config) { c.BaseURL += "?token=secret" }, appsbooks.ErrConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.change(&cfg)
			if _, err := appsbooks.New(cfg); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	base.BaseURL, base.Clock, base.HTTPClient = "", nil, nil
	base.SToken = base64.StdEncoding.EncodeToString([]byte(`{"token":"secret","expDate":"2099-01-01T00:00:00+0000"}`))
	if _, err := appsbooks.New(base); err != nil {
		t.Fatal("offline default construction", err)
	}
}

// TestInvalidClientConfigurationNeverPosted checks that invalid client configuration never posted.
func TestInvalidClientConfigurationNeverPosted(t *testing.T) {
	client, _ := protocolClient(t, nil, nil)
	for _, tc := range []struct {
		name   string
		change func(*appsbooks.ClientConfigurationRequest)
		want   error
	}{
		{"different MDM", func(c *appsbooks.ClientConfigurationRequest) { c.MDMInfo.ID = "other" }, appsbooks.ErrInput},
		{"long name", func(c *appsbooks.ClientConfigurationRequest) { c.MDMInfo.Name = strings.Repeat("x", 101) }, appsbooks.ErrLimit},
		{"long token", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationAuthToken = strings.Repeat("x", 513) }, appsbooks.ErrLimit},
		{"unpaired credentials", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationURL = "" }, appsbooks.ErrInput},
		{"types without endpoint", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationURL, c.NotificationAuthToken = "", "" }, appsbooks.ErrInput},
		{"HTTP endpoint", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationURL = "http://example.com" }, appsbooks.ErrInput},
		{"query credentials", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationURL += "?token=secret" }, appsbooks.ErrInput},
		{"token whitespace", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationAuthToken = "unsafe token" }, appsbooks.ErrInput},
		{"unknown notification", func(c *appsbooks.ClientConfigurationRequest) { c.NotificationTypes = []string{"UNKNOWN"} }, appsbooks.ErrInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := appsbooks.ClientConfigurationRequest{
				MDMInfo:         appsbooks.MDMInfo{ID: "ours", Name: "Example", Metadata: "1"},
				NotificationURL: "https://example.com/notify", NotificationAuthToken: "secret", NotificationTypes: []string{"TEST_NOTIFICATION"},
			}
			tc.change(&in)
			if _, err := client.SetClientConfig(t.Context(), in); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestMutationValidationNeverPosted checks that mutation validation never posted.
func TestMutationValidationNeverPosted(t *testing.T) {
	client, _ := protocolClient(t, nil, nil)
	asset := appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}
	for _, tc := range []struct {
		name string
		in   appsbooks.ManageAssetsRequest
		want error
	}{
		{"empty", appsbooks.ManageAssetsRequest{}, appsbooks.ErrInput},
		{"too many assets", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset, asset, asset}}, appsbooks.ErrLimit},
		{"missing AdamID", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{PricingParam: "STDQ"}}}, appsbooks.ErrInput},
		{"bad pricing", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{AdamID: "1", PricingParam: "unknown"}}}, appsbooks.ErrInput},
		{"duplicate asset", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset, asset}}, appsbooks.ErrInput},
		{"no targets", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset}}, appsbooks.ErrInput},
		{"empty target", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset}, SerialNumbers: []string{""}}, appsbooks.ErrInput},
		{"duplicate target", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset}, ClientUserIDs: []string{"U", "U"}}, appsbooks.ErrInput},
		{"too many users", appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{asset}, ClientUserIDs: []string{"1", "2", "3"}}, appsbooks.ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Associate(t.Context(), tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := client.Revoke(t.Context(), appsbooks.RevokeAssetsRequest{}); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		users []appsbooks.RequestUser
		want  error
	}{
		{nil, appsbooks.ErrInput},
		{[]appsbooks.RequestUser{{ClientUserID: "1"}, {ClientUserID: "2"}, {ClientUserID: "3"}}, appsbooks.ErrLimit},
		{[]appsbooks.RequestUser{{ClientUserID: "1"}, {ClientUserID: "1"}}, appsbooks.ErrInput},
	} {
		if _, err := client.CreateUsers(t.Context(), appsbooks.ManageUsersRequest{Users: tc.users}); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
}

// TestDiscoveryValidationAndCopies checks discovery validation and copies.
func TestDiscoveryValidationAndCopies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsbooks.ServiceConfiguration)
	}{
		{"missing rate", func(s *appsbooks.ServiceConfiguration) { delete(s.Limits, "maxRequestPerSecond") }},
		{"cross host", func(s *appsbooks.ServiceConfiguration) { s.URLs["getAssets"] = "https://other.example.com/assets" }},
		{"insecure", func(s *appsbooks.ServiceConfiguration) { s.URLs["getAssets"] = "http://example.com/assets" }},
		{"query", func(s *appsbooks.ServiceConfiguration) { s.URLs["getAssets"] += "?unexpected=true" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := protocolClient(t, tc.change, nil)
			if _, err := client.Assets(t.Context(), appsbooks.AssetsQuery{}); !errors.Is(err, appsbooks.ErrProtocol) {
				t.Fatal(err)
			}
		})
	}
	client, _ := protocolClient(t, func(s *appsbooks.ServiceConfiguration) { delete(s.Limits, "maxAssets") }, nil)
	if _, err := client.Associate(t.Context(), appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{AdamID: "1", PricingParam: "STDQ"}}, SerialNumbers: []string{"D"}}); !errors.Is(err, appsbooks.ErrProtocol) {
		t.Fatal(err)
	}
	for _, template := range []string{"https://example.com/no-placeholder", "http://example.com/?code=%25inviteCode%25"} {
		client, _ := protocolClient(t, func(s *appsbooks.ServiceConfiguration) { s.URLs["invitationEmail"] = template }, nil)
		if _, err := client.InvitationURL(t.Context(), "code"); !errors.Is(err, appsbooks.ErrProtocol) {
			t.Fatal(err)
		}
	}
	if _, err := client.InvitationURL(t.Context(), ""); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	first, err := client.ServiceConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.ErrorCodes[0].Info[0] = '!'
	second, err := client.ServiceConfig(t.Context())
	if err != nil || string(second.ErrorCodes[0].Info) != `{"detail":"original"}` {
		t.Fatal("nested service metadata aliases cache", err)
	}
}

type readFailure struct{ err error }

// Read returns the configured read failure without reading bytes.
func (r readFailure) Read([]byte) (int, error) { return 0, r.err }

// TestTransportErrorsRedactSecretsAndPreserveCause checks transport errors redact secrets and
// preserve cause.
func TestTransportErrorsRedactSecretsAndPreserveCause(t *testing.T) {
	cause := errors.New("private-content-token in transport diagnostic")
	for _, bodyFailure := range []bool{false, true} {
		_, cfg := protocolClient(t, nil, nil)
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if !bodyFailure {
				return nil, cause
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(readFailure{cause})}, nil
		})}
		client, err := appsbooks.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []func() error{
			func() error { _, err := client.ServiceConfig(t.Context()); return err },
			func() error { _, err := client.InvitationURL(t.Context(), "code"); return err },
			func() error {
				_, err := client.WalkAssets(t.Context(), appsbooks.AssetsQuery{}, 1, func(appsbooks.AssetRecord) error { return nil })
				return err
			},
		} {
			err := call()
			var transport *appsbooks.TransportError
			if !errors.As(err, &transport) || !errors.Is(err, cause) || strings.Contains(err.Error(), "private-content-token") {
				t.Fatalf("transport error contract: %v", err)
			}
		}
	}
}

// TestResponseAndEventValidation checks response and event validation.
func TestResponseAndEventValidation(t *testing.T) {
	for _, body := range []string{"{", `{"uId":"L","assets":true}`, `{"assets":[]}`, strings.Repeat("x", 32<<20+1)} {
		client, _ := protocolClient(t, nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		if _, err := client.Assets(t.Context(), appsbooks.AssetsQuery{}); !errors.Is(err, appsbooks.ErrProtocol) {
			t.Fatal(err)
		}
	}
	client, _ := protocolClient(t, nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"uId":"L"}`) })
	if _, err := client.CreateUsers(t.Context(), appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "U"}}}); !errors.Is(err, appsbooks.ErrProtocol) {
		t.Fatal("missing event accepted", err)
	}
	if _, err := client.EventStatus(t.Context(), ""); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	for _, body := range []string{`{"uId":"L"}`, `{"uId":"L","eventStatus":"COMPLETE","numRequested":1,"numCompleted":2}`, `{"uId":"L","eventStatus":"PENDING","numCompleted":-1}`} {
		client, _ := protocolClient(t, nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		if _, err := client.EventStatus(t.Context(), "E"); !errors.Is(err, appsbooks.ErrProtocol) {
			t.Fatal(err)
		}
	}
	for _, email := range []string{string([]byte{0xff}), strings.Repeat("x", 32<<20)} {
		if _, err := client.CreateUsers(t.Context(), appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "U", Email: email}}}); !errors.Is(err, appsbooks.ErrInput) {
			t.Fatal("invalid request encoded", err)
		}
	}
}

// TestQueryFiltersAndVisitorFailures checks query filters and visitor failures.
func TestQueryFiltersAndVisitorFailures(t *testing.T) {
	client, _ := protocolClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("minAvailableCount") != "0" || r.URL.Query().Get("deviceAssignable") != "false" || r.URL.Query().Get("revocable") != "true" {
			t.Error("missing explicit zero/false filters", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"uId":"L","assets":[{"adamId":"1"}],"versionId":"v1"}`)
	})
	q := appsbooks.AssetsQuery{MinAvailableCount: new(int64(0)), DeviceAssignable: new(false), Revocable: new(true)}
	stop := errors.New("visitor failed")
	if version, err := client.WalkAssets(t.Context(), q, 2, func(appsbooks.AssetRecord) error { return stop }); version != "" || !errors.Is(err, stop) {
		t.Fatal("failed walk advanced checkpoint", version, err)
	}
	for _, maxPages := range []int{0, -1} {
		if _, err := client.WalkAssets(t.Context(), q, maxPages, func(appsbooks.AssetRecord) error { return nil }); !errors.Is(err, appsbooks.ErrInput) {
			t.Fatal(err)
		}
	}
	if _, err := client.WalkAssets(t.Context(), q, 1, nil); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := client.Assets(t.Context(), appsbooks.AssetsQuery{MinAvailableCount: new(int64(-1))}); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := client.Users(t.Context(), appsbooks.UsersQuery{ActiveOnly: new(true), RetiredOnly: new(true)}); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := client.Assignments(t.Context(), appsbooks.AssignmentsQuery{PageIndex: -1}); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
}

// TestNotificationRejectsUnauthenticatedAndMalformedInput checks that notification rejects
// unauthenticated and malformed input.
func TestNotificationRejectsUnauthenticatedAndMalformedInput(t *testing.T) {
	cause := errors.New("body unavailable")
	for _, tc := range []struct {
		name       string
		change     func(*http.Request)
		token, uid string
		want       error
	}{
		{"missing token", func(*http.Request) {}, "", "L", appsbooks.ErrNotificationAuth},
		{"missing location", func(*http.Request) {}, "secret", "", appsbooks.ErrNotificationAuth},
		{"wrong method", func(r *http.Request) { r.Method = "GET" }, "secret", "L", appsbooks.ErrNotificationAuth},
		{"missing header", func(r *http.Request) { r.Header.Del("Authorization") }, "secret", "L", appsbooks.ErrNotificationAuth},
		{"duplicate header", func(r *http.Request) { r.Header.Add("Authorization", "Bearer secret") }, "secret", "L", appsbooks.ErrNotificationAuth},
		{"missing body", func(r *http.Request) { r.Body = nil }, "secret", "L", appsbooks.ErrProtocol},
		{"read error", func(r *http.Request) { r.Body = io.NopCloser(readFailure{cause}) }, "secret", "L", cause},
		{"missing identity", func(*http.Request) {}, "secret", "L", appsbooks.ErrProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "POST", "https://example.com", strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer secret")
			tc.change(r)
			if _, err := appsbooks.DecodeNotification(r, tc.token, tc.uid); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	if _, err := appsbooks.DecodeNotification(nil, "secret", "L"); !errors.Is(err, appsbooks.ErrNotificationAuth) {
		t.Fatal(err)
	}
	var notification appsbooks.Notification
	var event appsbooks.AssetManagementNotification
	if err := notification.Decode(&event); !errors.Is(err, appsbooks.ErrProtocol) {
		t.Fatal(err)
	}
}

// TestRetryAfterDateAndReadRetryBound checks retry after date and read retry bound.
func TestRetryAfterDateAndReadRetryBound(t *testing.T) {
	for _, retry := range []string{"Tue, 15 Sep 2026 00:00:03 GMT", "Mon, 14 Sep 2026 00:00:00 GMT", "invalid"} {
		reads := 0
		client, cfg := protocolClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			reads++
			w.Header().Set("Retry-After", retry)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"errorNumber":9603}`)
		})
		_, err := client.Assets(t.Context(), appsbooks.AssetsQuery{})
		var api *appsbooks.APIError
		if !errors.As(err, &api) || reads != cfg.ReadRetries+1 {
			t.Fatal("unbounded read retries", reads, err)
		}
	}
}

// Keep the operation surface together so ownership and cancellation guarantees
// are checked for every public family, including reads and administrative writes.
func clientOperations(client *appsbooks.Client) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"service":       func(ctx context.Context) error { _, err := client.ServiceConfig(ctx); return err },
		"configuration": func(ctx context.Context) error { _, err := client.ClientConfig(ctx); return err },
		"set configuration": func(ctx context.Context) error {
			_, err := client.SetClientConfig(ctx, appsbooks.ClientConfigurationRequest{})
			return err
		},
		"assets": func(ctx context.Context) error { _, err := client.Assets(ctx, appsbooks.AssetsQuery{}); return err },
		"associate": func(ctx context.Context) error {
			_, err := client.Associate(ctx, appsbooks.ManageAssetsRequest{})
			return err
		},
		"revoke": func(ctx context.Context) error {
			_, err := client.Revoke(ctx, appsbooks.RevokeAssetsRequest{})
			return err
		},
		"users": func(ctx context.Context) error {
			_, err := client.CreateUsers(ctx, appsbooks.ManageUsersRequest{})
			return err
		},
		"event": func(ctx context.Context) error { _, err := client.EventStatus(ctx, "E"); return err },
	}
}

// TestCancellationWhileClientOccupied checks cancellation while client occupied.
func TestCancellationWhileClientOccupied(t *testing.T) {
	_, cfg := protocolClient(t, nil, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return nil, context.Canceled
	})}
	client, err := appsbooks.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := client.ServiceConfig(t.Context()); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, call := range clientOperations(client) {
		if err := call(ctx); !errors.Is(err, context.Canceled) {
			t.Error(name, err)
		}
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// TestMutationsRequireLocationOwner checks mutations require location owner.
func TestMutationsRequireLocationOwner(t *testing.T) {
	for _, body := range []string{`{"uId":"L"}`, `{"uId":"L","mdmInfo":{"id":"other"}}`} {
		_, cfg := protocolClient(t, nil, nil)
		original := cfg.HTTPClient.Transport
		cfg.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/client/config" {
				return original.RoundTrip(r)
			}
			if r.Method != http.MethodGet {
				t.Error("unowned mutation sent")
			}
			w := httptest.NewRecorder()
			_, _ = io.WriteString(w, body)
			return w.Result(), nil
		})
		client, err := appsbooks.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for name, call := range clientOperations(client) {
			if name != "associate" && name != "revoke" && name != "users" {
				continue
			}
			if err := call(t.Context()); !errors.Is(err, appsbooks.ErrOwnership) {
				t.Error(name, err)
			}
		}
	}
}
