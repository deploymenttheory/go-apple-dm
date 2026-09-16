package appsbooks_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// advancingClock records waits without introducing wall-clock races.
type advancingClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (c *advancingClock) Now() time.Time                  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *advancingClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }
func (c *advancingClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func (c *advancingClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	client      *appsbooks.Client
	clock       *advancingClock
	config      appsbooks.Config
	configReads atomic.Int32
}

func newFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	f := &fixture{clock: &advancingClock{now: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}}
	tokenData, err := json.Marshal(
		map[string]string{
			"token":   t.TempDir(),
			"expDate": "2030-11-08T22:33:22+0000",
			"orgName": "Example",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.StdEncoding.EncodeToString(tokenData)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/service/config" {
			f.configReads.Add(1)
			if r.Header.Get("Authorization") != "" {
				t.Error("service config received credentials")
			}
			urls := map[string]string{}
			for k, p := range map[string]string{"clientConfig": "client/config", "getAssets": "assets", "getAssignments": "assignments", "getUsers": "users", "associateAssets": "assets/associate", "disassociateAssets": "assets/disassociate", "revokeAssets": "assets/revoke", "createUsers": "users/create", "updateUsers": "users/update", "retireUsers": "users/retire", "eventStatus": "status"} {
				urls[k] = srv.URL + "/" + p
			}
			urls["invitationEmail"] = "https://buy.itunes.apple.com/invite?inviteCode=%25inviteCode%25"
			_ = json.NewEncoder(w).
				Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxAssets": 2, "maxClientUserIds": 3, "maxSerialNumbers": 3, "maxRevokeClientUserIds": 2, "maxRevokeSerialNumbers": 2, "maxUsers": 2, "maxRequestPerSecond": 15, "maxMdmIdLength": 100, "maxMdmNameLength": 100, "maxMdmMetadataLength": 255, "maxNotificationLength": 512}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong bearer: must use outer sToken")
		}
		if r.URL.Path == "/client/config" {
			_, _ = fmt.Fprint(
				w,
				`{"uId":"L","mdmInfo":{"id":"ours","name":"Example","metadata":"1"}}`,
			)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	f.config = appsbooks.Config{
		SToken:      token,
		MDMID:       "ours",
		UID:         "L",
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		Clock:       f.clock,
		ReadRetries: 2,
	}
	f.client, err = appsbooks.New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLicensingAndUserLifecycle(t *testing.T) {
	seen := map[string]int{}
	var mu sync.Mutex
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/status" {
			if r.URL.Query().Get("eventId") != "event-1" {
				t.Error("event filter")
			}
			_, _ = fmt.Fprint(
				w,
				`{"uId":"L","mdmInfo":{"id":"ours"},"eventStatus":"COMPLETE","eventType":"ASSOCIATE","numCompleted":1,"numRequested":2,"failures":[{"errorNumber":9709,"errorMessage":"private diagnostic","errorInfo":{"future":"kept"}}]}`,
			)
			return
		}
		if r.Method != "POST" {
			t.Error(r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if strings.HasPrefix(r.URL.Path, "/users/") {
			if _, ok := body["users"]; !ok {
				t.Error("missing users")
			}
		} else if r.URL.Path != "/assets/revoke" {
			if _, ok := body["assets"]; !ok {
				t.Error("missing assets")
			}
		}
		_, _ = fmt.Fprint(w, `{"eventId":"event-1","uId":"L","mdmInfo":{"id":"ours"}}`)
	})
	ctx := t.Context()
	assets := appsbooks.ManageAssetsRequest{
		Assets:        []appsbooks.Asset{{AdamID: "123", PricingParam: "STDQ"}},
		SerialNumbers: []string{"D"},
	}
	for _, call := range []func() (appsbooks.Event, error){
		func() (appsbooks.Event, error) { return f.client.Associate(ctx, assets) },
		func() (appsbooks.Event, error) {
			assets.SerialNumbers = nil
			assets.ClientUserIDs = []string{"U"}
			return f.client.Disassociate(ctx, assets)
		},
		func() (appsbooks.Event, error) {
			return f.client.Revoke(ctx, appsbooks.RevokeAssetsRequest{ClientUserIDs: []string{"U"}})
		},
		func() (appsbooks.Event, error) {
			return f.client.CreateUsers(ctx, appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "U", Email: "u@example.com"}}})
		},
		func() (appsbooks.Event, error) {
			return f.client.UpdateUsers(ctx, appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "U", ManagedAppleID: "u@example.com"}}})
		},
		func() (appsbooks.Event, error) {
			return f.client.RetireUsers(ctx, appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "U"}}})
		},
	} {
		e, err := call()
		if err != nil || e.EventID != "event-1" {
			t.Fatal(e, err)
		}
	}
	status, err := f.client.EventStatus(ctx, "event-1")
	if err != nil || status.Successful() || len(status.Failures) != 1 ||
		!strings.Contains(string(status.Failures[0].Info), "future") {
		t.Fatal(status, err)
	}
	status.NumCompleted = 2
	status.Failures = nil
	if !status.Successful() {
		t.Fatal(status)
	}
	link, err := f.client.InvitationURL(ctx, "a+b")
	if err != nil || !strings.Contains(link, "a%2Bb") {
		t.Fatal(link, err)
	}
	book := appsbooks.AssetRecord{ProductType: "Book", DeviceAssignable: true, Revocable: true}
	if book.CheckAssignment(false, false) != nil ||
		!errors.Is(book.CheckAssignment(true, false), appsbooks.ErrInput) ||
		!errors.Is(book.CheckAssignment(false, true), appsbooks.ErrInput) {
		t.Fatal("book restrictions")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 7 {
		t.Fatal(seen)
	}
}

func TestPagesAndDynamicLimits(t *testing.T) {
	var posts atomic.Int32
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			_, _ = fmt.Fprint(w, `{"eventId":"E","uId":"L"}`)
			return
		}
		q := r.URL.Query()
		if r.URL.Path == "/assignments" {
			if q.Get("sinceVersionId") != "old" || q.Get("clientUserId") != "U" {
				t.Error("lost filters")
			}
			if q.Get("pageIndex") == "0" {
				_, _ = fmt.Fprint(
					w,
					`{"assignments":[],"uId":"L","currentPageIndex":0,"nextPageIndex":1,"totalPages":1,"versionId":"first"}`,
				)
			} else {
				_, _ = fmt.Fprint(
					w,
					`{"assignments":[{"adamId":"123","pricingParam":"STDQ","clientUserId":"U"}],"uId":"L","currentPageIndex":1,"totalPages":2,"versionId":"changed"}`,
				)
			}
		} else if r.URL.Path == "/users" {
			_, _ = fmt.Fprint(
				w,
				`{"users":[{"clientUserId":"U","status":"Registered","inviteCode":"invite"}],"uId":"L","currentPageIndex":0,"totalPages":1,"versionId":"users-v"}`,
			)
		} else {
			_, _ = fmt.Fprint(
				w,
				`{"assets":[{"adamId":"123","pricingParam":"STDQ","productType":"Book","deviceAssignable":false,"revocable":false}],"uId":"L","currentPageIndex":0,"totalPages":1,"versionId":"assets-v"}`,
			)
		}
	})
	count := 0
	version, err := f.client.WalkAssignments(
		t.Context(),
		appsbooks.AssignmentsQuery{SinceVersionID: "old", ClientUserID: "U"},
		5,
		func(a appsbooks.Assignment) error { count++; return nil },
	)
	if err != nil || version != "first" || count != 1 {
		t.Fatal(version, count, err)
	}
	if version, err := f.client.WalkAssignments(
		t.Context(),
		appsbooks.AssignmentsQuery{SinceVersionID: "old", ClientUserID: "U"},
		1,
		func(appsbooks.Assignment) error { return nil },
	); version != "" ||
		!errors.Is(err, appsbooks.ErrLimit) {
		t.Fatal(version, err)
	}
	if _, err := f.client.WalkUsers(
		t.Context(),
		appsbooks.UsersQuery{},
		2,
		func(u appsbooks.User) error {
			if u.Status != "Registered" {
				t.Fatal(u)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.WalkAssets(
		t.Context(),
		appsbooks.AssetsQuery{},
		2,
		func(a appsbooks.AssetRecord) error {
			if a.ProductType != "Book" {
				t.Fatal(a)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	in := appsbooks.ManageAssetsRequest{
		Assets:        []appsbooks.Asset{{AdamID: "123", PricingParam: "STDQ"}},
		SerialNumbers: []string{"1", "2", "3", "4"},
	}
	if _, err := f.client.Associate(
		t.Context(),
		in,
	); !errors.Is(err, appsbooks.ErrLimit) ||
		posts.Load() != 0 {
		t.Fatal(err)
	}
	if f.configReads.Load() != 1 {
		t.Fatal("unexpected config refresh")
	}
	config, err := f.client.ServiceConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config.Limits["maxAssets"] = 999
	again, err := f.client.ServiceConfig(t.Context())
	if err != nil || again.Limits["maxAssets"] != 2 {
		t.Fatal("cache aliased", err)
	}
	f.clock.advance(5 * time.Minute)
	if _, err := f.client.ServiceConfig(t.Context()); err != nil || f.configReads.Load() != 2 {
		t.Fatal("missing refresh", err)
	}
}

func TestRetriesCancellationAndIdentity(t *testing.T) {
	var reads, posts atomic.Int32
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(503)
			_, _ = fmt.Fprint(
				w,
				`{"errorNumber":9603,"errorMessage":"inner-secret","errorInfo":{"future":true}}`,
			)
			return
		}
		if reads.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			_, _ = fmt.Fprint(w, `{"errorNumber":9646}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"uId":"L","currentPageIndex":0,"assets":[]}`)
	})
	if _, err := f.client.Assets(
		t.Context(),
		appsbooks.AssetsQuery{},
	); err != nil ||
		reads.Load() != 2 {
		t.Fatal(err)
	}
	f.clock.mu.Lock()
	found := false
	for _, d := range f.clock.waits {
		found = found || d >= 2*time.Second
	}
	f.clock.mu.Unlock()
	if !found {
		t.Fatal("ignored Retry-After")
	}
	_, err := f.client.Associate(
		t.Context(),
		appsbooks.ManageAssetsRequest{
			Assets:        []appsbooks.Asset{{AdamID: "1", PricingParam: "STDQ"}},
			SerialNumbers: []string{"D"},
		},
	)
	var api *appsbooks.APIError
	if !errors.As(err, &api) || posts.Load() != 1 || api.RetryAfter != 2*time.Second ||
		strings.Contains(err.Error(), "inner-secret") ||
		!strings.Contains(string(api.Info), "future") {
		t.Fatal("mutation replay/diagnostic", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.client.Assets(ctx, appsbooks.AssetsQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, response := range []struct {
		json string
		want error
	}{{`{"uId":"other","assets":[]}`, appsbooks.ErrLocation}, {`{"uId":"L","mdmInfo":{"id":"other"},"assets":[]}`, appsbooks.ErrOwnership}, {`{"uId":"L","currentPageIndex":0,"nextPageIndex":0}`, appsbooks.ErrProtocol}} {
		g := newFixture(
			t,
			func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, response.json) },
		)
		if _, err := g.client.Assets(
			t.Context(),
			appsbooks.AssetsQuery{},
		); !errors.Is(
			err,
			response.want,
		) {
			t.Fatal(err)
		}
	}
	f.clock.advance(10 * 365 * 24 * time.Hour)
	if _, err := f.client.Assets(
		t.Context(),
		appsbooks.AssetsQuery{},
	); !errors.Is(
		err,
		appsbooks.ErrExpired,
	) {
		t.Fatal(err)
	}
}

func TestNotification(t *testing.T) {
	const data = `{"notification":{"eventId":"E","result":"SUCCESS","type":"ASSOCIATE","assignments":[{"adamId":"1","pricingParam":"STDQ","clientUserId":"U"}]},"notificationId":"N","notificationType":"ASSET_MANAGEMENT","uId":"L"}`
	request := func(body, auth string) *http.Request {
		r := httptest.NewRequest(
			http.MethodPost,
			"https://example.com/notifications",
			strings.NewReader(body),
		)
		r.Header.Set("Authorization", auth)
		return r
	}
	n, err := appsbooks.DecodeNotification(request(data, "Bearer secret"), "secret", "L")
	if err != nil || n.ID != "N" || n.UID != "L" {
		t.Fatal(n, err)
	}
	var event appsbooks.AssetManagementNotification
	if err := n.Decode(&event); err != nil || event.EventID != "E" || len(event.Assignments) != 1 {
		t.Fatal(event, err)
	}
	if _, err := appsbooks.DecodeNotification(
		request(data, "Bearer wrong"),
		"secret",
		"L",
	); !errors.Is(
		err,
		appsbooks.ErrNotificationAuth,
	) {
		t.Fatal(err)
	}
	if _, err := appsbooks.DecodeNotification(
		request(data, "Bearer secret"),
		"secret",
		"other",
	); !errors.Is(
		err,
		appsbooks.ErrLocation,
	) {
		t.Fatal(err)
	}
	for _, kind := range []string{"TEST_NOTIFICATION", "FUTURE"} {
		body := `{"notification":{},"notificationId":"T","notificationType":"` + kind + `","uId":"L"}`
		n, err := appsbooks.DecodeNotification(request(body, "Bearer secret"), "secret", "L")
		if err != nil || n.Type != kind {
			t.Fatal(n, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestConfigurationOwnershipAndRedirects(t *testing.T) {
	for _, owner := range []string{"", "ours", "other"} {
		t.Run("owner="+owner, func(t *testing.T) {
			f := newFixture(
				t,
				func(http.ResponseWriter, *http.Request) { t.Error("unexpected endpoint") },
			)
			var gets, posts int
			hc := *f.config.HTTPClient
			original := hc.Transport
			hc.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/client/config" {
					return original.RoundTrip(r)
				}
				w := httptest.NewRecorder()
				if r.Method == "GET" {
					gets++
					if owner == "" {
						fmt.Fprint(w, `{"uId":"L"}`)
					} else {
						fmt.Fprintf(w, `{"uId":"L","mdmInfo":{"id":%q}}`, owner)
					}
				} else {
					posts++
					if gets != 1 {
						t.Error("mutation before ownership read")
					}
					var body appsbooks.ClientConfigurationRequest
					if err := json.NewDecoder(r.Body).
						Decode(&body); err != nil || body.MDMInfo.ID != "ours" ||
						body.NotificationURL != "https://notify.example.com/vpp" {
						t.Error("configuration body", err)
					}
					fmt.Fprint(w, `{"uId":"L","mdmInfo":{"id":"ours"}}`)
				}
				return w.Result(), nil
			})
			f.config.HTTPClient = &hc
			client, err := appsbooks.New(f.config)
			if err != nil {
				t.Fatal(err)
			}
			request := appsbooks.ClientConfigurationRequest{
				MDMInfo: appsbooks.MDMInfo{
					ID:       "ours",
					Name:     "Example",
					Metadata: "1",
				},
				NotificationURL:       "https://notify.example.com/vpp",
				NotificationAuthToken: "notify-secret",
				NotificationTypes:     []string{"ASSET_MANAGEMENT"},
			}
			_, err = client.SetClientConfig(t.Context(), request)
			if owner == "other" {
				if !errors.Is(err, appsbooks.ErrOwnership) || posts != 0 {
					t.Fatal("foreign owner overwritten", err)
				}
			} else if err != nil || posts != 1 {
				t.Fatal("configuration failed", err)
			}
		})
	}
	var followed atomic.Int32
	target := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { followed.Add(1); w.WriteHeader(200) },
		),
	)
	defer target.Close()
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, err := f.client.Assets(t.Context(), appsbooks.AssetsQuery{})
	if err == nil || followed.Load() != 0 {
		t.Fatal("redirect followed", err)
	}
}

func TestConcurrentConfigurationAndTokenIsolation(t *testing.T) {
	fixtures := []*fixture{
		newFixture(
			t,
			func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"uId":"L"}`) },
		),
		newFixture(
			t,
			func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"uId":"L"}`) },
		),
	}
	var wg sync.WaitGroup
	for _, f := range fixtures {
		for range 8 {
			wg.Go(func() {
				if _, err := f.client.ClientConfig(t.Context()); err != nil {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	for _, f := range fixtures {
		if f.configReads.Load() != 1 {
			t.Fatal("configuration cache is not per-client", f.configReads.Load())
		}
	}
}
