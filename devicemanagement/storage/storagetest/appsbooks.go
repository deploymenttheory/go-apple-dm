package storagetest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// AppsBooksFactory supplies independent handles sharing one atomic backend. Each
// invocation starts with empty state; implementations own database isolation.
type AppsBooksFactory func(*testing.T) (state.Store, state.Store)

// appsBooksService provides controlled source pages and barriers before HTTP
// responses, allowing independent backend handles to exercise stale work.
type appsBooksService struct {
	mu          sync.Mutex
	owner       string
	uid         string
	fail        string
	pageFailure bool
	posts       int
	seq         int
	blockPath   string
	entered     chan struct{}
	release     chan struct{}
	queries     []string
	tokens      map[string]string
}

// token returns an outer sToken containing a unique fixture bearer.
func appsBooksToken(value, expires string) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"token":%q,"expDate":%q}`, value, expires)))
}

// fixtureAppsBooks creates a real TLS HTTP client using an accelerated clock.
func fixtureAppsBooks(t *testing.T) (*appsBooksService, appsbooks.Config, string) {
	t.Helper()
	f := &appsBooksService{owner: "ours", uid: "UID", seq: 1, tokens: map[string]string{}}
	token := appsBooksToken("initial", "2035-01-01T00:00:00Z")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/service/config" {
			urls := map[string]string{}
			for key, path := range map[string]string{"clientConfig": "/config", "getAssets": "/assets", "getAssignments": "/assignments", "getUsers": "/users"} {
				urls[key] = server.URL + path
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxRequestPerSecond": 100000, "maxMdmIdLength": 100, "maxMdmNameLength": 100, "maxMdmMetadataLength": 100, "maxNotificationLength": 100}}); err != nil {
				t.Error(err)
			}
			return
		}
		f.mu.Lock()
		uid := f.uid
		if v, ok := f.tokens[r.Header.Get("Authorization")]; ok {
			uid = v
		}
		owner, fail, seq, pageFailure := f.owner, f.fail, f.seq, f.pageFailure
		f.queries = append(f.queries, r.Method+" "+r.URL.RequestURI())
		block := f.blockPath == r.URL.Path
		entered, release := f.entered, f.release
		if block {
			f.blockPath = ""
		}
		if r.Method == http.MethodPost {
			f.posts++
		}
		f.mu.Unlock()
		if block {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if fail == r.URL.Path || (pageFailure && r.URL.Query().Get("pageIndex") == "1") {
			http.Error(w, `{"errorNumber":9600}`, http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost {
			var in appsbooks.ClientConfigurationRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				return
			}
			owner = in.MDMInfo.ID
			f.mu.Lock()
			f.owner = owner
			f.mu.Unlock()
		}
		meta := fmt.Sprintf(`"uId":%q`, uid)
		if owner != "" {
			meta += fmt.Sprintf(`,"mdmInfo":{"id":%q}`, owner)
		}
		if r.URL.Path == "/config" {
			if _, err := fmt.Fprintf(w, `{%s,"locationName":"London","notificationAuthToken":"private-notification","future":{"number":9007199254740993,"null":null,"false":false}}`, meta); err != nil {
				t.Error(err)
			}
			return
		}
		index, _ := strconv.Atoi(r.URL.Query().Get("pageIndex"))
		next := ""
		if index == 0 {
			next = `,"nextPageIndex":1`
		}
		kind := strings.TrimPrefix(r.URL.Path, "/")
		value := `[]`
		if index == 1 {
			switch kind {
			case "assets":
				value = `[{"adamId":"1","pricingParam":"STDQ","totalCount":2147483647,"future":{"n":9007199254740993,"null":null,"false":false}}]`
			case "assignments":
				value = `[{"adamId":"1","pricingParam":"STDQ","clientUserId":"user","future":null}]`
			case "users":
				value = `[{"clientUserId":"user","status":"Registered","future":false}]`
			}
		}
		response := map[string]any{"uId": uid, "currentPageIndex": index, "totalPages": 2, "size": 0, "versionId": fmt.Sprintf("%s-%d-%d", kind, seq, index), kind: json.RawMessage(value), "future": json.RawMessage(`{"n":9007199254740993,"null":null,"false":false}`)}
		if next != "" {
			response["nextPageIndex"] = 1
		}
		if owner != "" {
			response["mdmInfo"] = map[string]string{"id": owner}
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	cfg := appsbooks.Config{MDMID: "ours", BaseURL: server.URL, HTTPClient: server.Client(), Clock: &appsBooksClock{Fake: clock.NewFake(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}}
	return f, cfg, token
}

// newAppsBooksWorkflow fails setup immediately rather than concealing constructor errors.
func newAppsBooksWorkflow(t *testing.T, s state.Store, cfg appsbooks.Config) *appsbooks.Workflow {
	t.Helper()
	w, err := appsbooks.NewWorkflow(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// requireAppsBooksLocation reads a consistent snapshot for evidence comparisons.
func requireAppsBooksLocation(t *testing.T, w *appsbooks.Workflow, id string) (appsbooks.Location, appsbooks.LocationEvidence) {
	t.Helper()
	l, e, err := w.LoadEvidence(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return l, e
}

// RunAppsBooks exercises workflow atomicity, isolation and fencing against both
// caller-owned memory and sealed SQL. It sends requests only to its TLS fixture.
func RunAppsBooks(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	t.Run("ReadOnlyRegistrationOwnership", func(t *testing.T) {
		a, _ := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w := newAppsBooksWorkflow(t, a, cfg)
		f.mu.Lock()
		f.owner = "foreign"
		f.mu.Unlock()
		if _, err := w.Register(t.Context(), "Foreign", 0, token); !errors.Is(err, appsbooks.ErrOwnership) {
			t.Fatal(err)
		}
		if _, err := w.Load(t.Context(), "Foreign"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign read persisted ownership", err)
		}
		f.mu.Lock()
		f.owner = ""
		f.mu.Unlock()
		l, err := w.Register(t.Context(), "Unclaimed", 0, token)
		if err != nil || l.OwnerID != "" {
			t.Fatal(l, err)
		}
		l, err = w.Probe(t.Context(), l.ID, l.Revision)
		if err != nil || l.OwnerID != "" {
			t.Fatal(l, err)
		}
		f.mu.Lock()
		posts := f.posts
		f.mu.Unlock()
		if posts != 0 {
			t.Fatal("read-only setup claimed ownership")
		}
	})
	t.Run("SnapshotsCheckpointsAndFailure", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w := newAppsBooksWorkflow(t, a, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		l, err = w.Refresh(t.Context(), l.ID, l.Revision, 3)
		if err != nil {
			t.Fatal(err)
		}
		_, e := requireAppsBooksLocation(t, w, l.ID)
		if e.Assets.VersionID != "assets-1-0" || len(e.Assets.Pages) != 2 || len(e.Assignments) != 1 || e.Assignments[0].VersionID != "assignments-1-0" || e.Users[0].VersionID != "users-1-0" {
			t.Fatalf("checkpoint/evidence: %+v", e)
		}
		if !strings.Contains(string(e.Assets.Pages[1]), `9007199254740993`) || !strings.Contains(string(e.Configuration), `"null":null,"false":false`) {
			t.Fatal("raw source changed")
		}
		restart := newAppsBooksWorkflow(t, b, cfg)
		for _, path := range []string{"/assets", "/assignments", "/users"} {
			f.mu.Lock()
			f.fail = path
			f.mu.Unlock()
			if _, err := restart.Refresh(t.Context(), l.ID, l.Revision, 3); err == nil {
				t.Fatal("failure accepted", path)
			}
			got, kept := requireAppsBooksLocation(t, restart, l.ID)
			if !reflect.DeepEqual(l, got) || !reflect.DeepEqual(e, kept) {
				t.Fatal("failed refresh changed successful evidence")
			}
		}
		f.mu.Lock()
		f.fail = ""
		f.pageFailure = true
		f.mu.Unlock()
		if _, err := restart.Refresh(t.Context(), l.ID, l.Revision, 3); err == nil {
			t.Fatal("page failure accepted")
		}
		_, kept := requireAppsBooksLocation(t, restart, l.ID)
		if !reflect.DeepEqual(e, kept) {
			t.Fatal("partial traversal published")
		}
		f.mu.Lock()
		f.pageFailure = false
		f.seq = 2
		f.queries = nil
		f.mu.Unlock()
		l, err = restart.Refresh(t.Context(), l.ID, l.Revision, 3)
		if err != nil {
			t.Fatal(err)
		}
		_, e = requireAppsBooksLocation(t, restart, l.ID)
		if len(e.Assignments) != 2 || e.Assignments[1].SinceVersionID != "assignments-1-0" || e.Users[1].SinceVersionID != "users-1-0" || e.Assets.VersionID != "assets-2-0" {
			t.Fatal("incremental/full contract", e)
		}
		f.mu.Lock()
		queries := strings.Join(f.queries, "\n")
		f.mu.Unlock()
		if strings.Contains(queries, "unlimited=") || strings.Contains(queries, "assets?pageIndex=0&since") || !strings.Contains(queries, "sinceVersionId=assignments-1-0") || !strings.Contains(queries, "sinceVersionId=users-1-0") {
			t.Fatal(queries)
		}
		if _, err := restart.Refresh(t.Context(), l.ID, l.Revision, 1); !errors.Is(err, appsbooks.ErrLimit) {
			t.Fatal(err)
		}
	})
	t.Run("IdentityExpiryAndIsolation", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w := newAppsBooksWorkflow(t, a, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		original, e := requireAppsBooksLocation(t, w, l.ID)
		f.mu.Lock()
		f.owner = "foreign"
		f.mu.Unlock()
		for _, call := range []func() error{
			func() error { _, err := w.Probe(t.Context(), l.ID, l.Revision); return err },
			func() error {
				_, err := w.Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "1"}})
				return err
			},
			func() error { _, err := w.Register(t.Context(), l.ID, l.Revision, token); return err },
		} {
			if err := call(); !errors.Is(err, appsbooks.ErrOwnership) {
				t.Fatal(err)
			}
		}
		f.mu.Lock()
		posts := f.posts
		f.owner = "ours"
		f.uid = "WRONG"
		f.mu.Unlock()
		if posts != 0 {
			t.Fatal("foreign owner was mutated")
		}
		if _, err := w.Register(t.Context(), l.ID, l.Revision, token); !errors.Is(err, appsbooks.ErrLocation) {
			t.Fatal(err)
		}
		other := newAppsBooksWorkflow(t, b, cfg)
		otherToken := appsBooksToken("other-location", "2035-01-01T00:00:00Z")
		f.mu.Lock()
		f.uid = "UID"
		f.tokens["Bearer "+otherToken] = "OTHER"
		f.mu.Unlock()
		alias, err := other.Register(t.Context(), "Alias", 0, otherToken)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Register(t.Context(), l.ID, l.Revision, otherToken); !errors.Is(err, appsbooks.ErrLocation) {
			t.Fatal("location accepted another location's credential", err)
		}
		if alias, err = other.Probe(t.Context(), alias.ID, alias.Revision); err != nil {
			t.Fatal("separate location credential was mixed", err)
		}
		if _, err := other.Register(t.Context(), "Duplicate", 0, token); !errors.Is(err, appsbooks.ErrLocation) {
			t.Fatal("duplicate Apple library", err)
		}
		got, kept := requireAppsBooksLocation(t, w, l.ID)
		if !reflect.DeepEqual(original, got) || !reflect.DeepEqual(e, kept) {
			t.Fatal("rejected identity changed data")
		}
		changed := cfg
		changed.MDMID = "different"
		if _, err := newAppsBooksWorkflow(t, b, changed).Load(t.Context(), l.ID); !errors.Is(err, appsbooks.ErrOwnership) {
			t.Fatal(err)
		}
		expired := appsBooksToken("expired", "2020-01-01T00:00:00Z")
		if _, err := w.Register(t.Context(), l.ID, l.Revision, expired); !errors.Is(err, appsbooks.ErrExpired) {
			t.Fatal(err)
		}
		renewal := appsBooksToken("renewal", "2040-01-01T00:00:00Z")
		l, err = w.Register(t.Context(), l.ID, l.Revision, renewal)
		if err != nil || l.CredentialRevision <= original.CredentialRevision || !l.TokenExpiresAt.After(original.TokenExpiresAt) {
			t.Fatal(l, err)
		}
		if _, err := other.Probe(t.Context(), l.ID, original.Revision); !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal(err)
		}
		clk, ok := cfg.Clock.(*appsBooksClock)
		if !ok {
			t.Fatal("fixture clock has unexpected type")
		}
		clk.Advance(6 * 365 * 24 * time.Hour)
		if _, err := other.Probe(t.Context(), "Alias", alias.Revision); !errors.Is(err, appsbooks.ErrExpired) {
			t.Fatal(err)
		}
		if _, err := w.Probe(t.Context(), l.ID, l.Revision); err != nil {
			t.Fatal("renewed credential expired with old credential", err)
		}
		lateRenewal := appsBooksToken("late-renewal", "2040-01-01T00:00:00Z")
		f.mu.Lock()
		f.tokens["Bearer "+lateRenewal] = "OTHER"
		f.mu.Unlock()
		if _, err := other.Register(t.Context(), alias.ID, alias.Revision, lateRenewal); err != nil {
			t.Fatal("expired location cannot renew", err)
		}
	})
	t.Run("PendingConfigurationAndRestart", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w := newAppsBooksWorkflow(t, a, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.owner = ""
		f.mu.Unlock()
		in := appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Our MDM", Metadata: "stable"}}
		l, err = w.Configure(t.Context(), l.ID, l.Revision, in)
		if err != nil || l.ConfigurationPending {
			t.Fatal(l, err)
		}
		// A cancelled POST is uncertain. Inject a transport error after the read and
		// durable reservation; a stopped process can then recover with a GET.
		transport := cfg.HTTPClient.Transport
		broken := *cfg.HTTPClient
		broken.Transport = appsBooksRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return nil, errors.New("uncertain POST")
			}
			return transport.RoundTrip(r)
		})
		badCfg := cfg
		badCfg.HTTPClient = &broken
		if _, err := newAppsBooksWorkflow(t, b, badCfg).Configure(t.Context(), l.ID, l.Revision, in); err == nil {
			t.Fatal("POST failure accepted")
		}
		l, e := requireAppsBooksLocation(t, w, l.ID)
		if !l.ConfigurationPending {
			t.Fatal("unfinished operation not durable")
		}
		restarted := newAppsBooksWorkflow(t, b, cfg)
		if _, err := restarted.Register(t.Context(), l.ID, l.Revision, token); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			t.Fatal(err)
		}
		if _, err := restarted.Configure(t.Context(), l.ID, l.Revision, in); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			t.Fatal(err)
		}
		if _, err := restarted.Refresh(t.Context(), l.ID, l.Revision, 3); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			t.Fatal(err)
		}
		l, err = restarted.Probe(t.Context(), l.ID, l.Revision)
		if err != nil || l.ConfigurationPending {
			t.Fatal(l, err)
		}
		_, kept := requireAppsBooksLocation(t, restarted, l.ID)
		if !reflect.DeepEqual(e.Assets, kept.Assets) || !reflect.DeepEqual(e.Assignments, kept.Assignments) {
			t.Fatal("probe changed refresh evidence")
		}
	})

	t.Run("ExpiredPendingConfigurationRecovery", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		l, err = w.Refresh(t.Context(), l.ID, l.Revision, 3)
		if err != nil {
			t.Fatal(err)
		}
		// Hold old read-only work across pending reservation, expiry and recovery.
		// The original mutating call below terminates before recovery begins.
		f.mu.Lock()
		f.blockPath = "/config"
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
		entered, release := f.entered, f.release
		f.mu.Unlock()
		stale := make(chan error, 1)
		staleRevision, id := l.Revision, l.ID
		go func() { _, err := w.Probe(t.Context(), id, staleRevision); stale <- err }()
		<-entered
		defer func() {
			close(release)
			if err := <-stale; !errors.Is(err, appsbooks.ErrRevision) {
				t.Errorf("old work was not fenced: %v", err)
			}
		}()
		transport := cfg.HTTPClient.Transport
		broken := *cfg.HTTPClient
		attempts := 0
		broken.Transport = appsBooksRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				attempts++
				return nil, errors.New("stopped uncertain POST")
			}
			return transport.RoundTrip(r)
		})
		badCfg := cfg
		badCfg.HTTPClient = &broken
		if _, err := newAppsBooksWorkflow(t, b, badCfg).Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "stable"}}); err == nil {
			t.Fatal("uncertain POST accepted")
		}
		l, e := requireAppsBooksLocation(t, other, l.ID)
		if !l.ConfigurationPending || attempts != 1 {
			t.Fatal("pending reservation missing", l, attempts)
		}
		clk, ok := cfg.Clock.(*appsBooksClock)
		if !ok {
			t.Fatal("unexpected fixture clock")
		}
		clk.Advance(6 * 365 * 24 * time.Hour)
		replacement := appsBooksToken("recovery", "2040-01-01T00:00:00Z")
		if _, err := other.Probe(t.Context(), l.ID, l.Revision); !errors.Is(err, appsbooks.ErrExpired) {
			t.Fatal(err)
		}
		if _, err := other.Register(t.Context(), l.ID, l.Revision, replacement); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			t.Fatal(err)
		}
		for _, failure := range []string{"uid", "owner", "probe", "revision"} {
			f.mu.Lock()
			switch failure {
			case "uid":
				f.uid = "WRONG"
			case "owner":
				f.owner = "foreign"
			case "probe":
				f.fail = "/config"
			}
			f.mu.Unlock()
			revision := l.Revision
			expected := error(nil)
			switch failure {
			case "uid":
				expected = appsbooks.ErrLocation
			case "owner":
				expected = appsbooks.ErrOwnership
			case "revision":
				revision--
				expected = appsbooks.ErrRevision
			}
			_, err := other.RecoverConfiguration(t.Context(), l.ID, revision, replacement)
			if err == nil || (expected != nil && !errors.Is(err, expected)) {
				t.Fatalf("%s: %v", failure, err)
			}
			got, kept := requireAppsBooksLocation(t, other, l.ID)
			if !reflect.DeepEqual(l, got) || !reflect.DeepEqual(e, kept) {
				t.Fatal("failed recovery changed pending evidence", failure)
			}
			f.mu.Lock()
			f.uid = "UID"
			f.owner = "ours"
			f.fail = ""
			f.mu.Unlock()
		}
		recovered, err := other.RecoverConfiguration(t.Context(), l.ID, l.Revision, replacement)
		if err != nil || recovered.ConfigurationPending || recovered.CredentialRevision <= l.CredentialRevision || recovered.Revision <= l.Revision || !recovered.TokenExpiresAt.After(l.TokenExpiresAt) {
			t.Fatal(recovered, err)
		}
		_, kept := requireAppsBooksLocation(t, other, l.ID)
		if !reflect.DeepEqual(e.Assets, kept.Assets) || !reflect.DeepEqual(e.Assignments, kept.Assignments) || !reflect.DeepEqual(e.Users, kept.Users) {
			t.Fatal("recovery changed source journals/checkpoints")
		}
		if _, err := other.RecoverConfiguration(t.Context(), l.ID, l.Revision, replacement); !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal("old recovery revision accepted", err)
		}
		if _, err := other.RecoverConfiguration(t.Context(), l.ID, recovered.Revision, replacement); !errors.Is(err, appsbooks.ErrInput) {
			t.Fatal("recovery accepted without pending reservation", err)
		}
		restarted := newAppsBooksWorkflow(t, b, cfg)
		if _, err := restarted.Refresh(t.Context(), l.ID, recovered.Revision, 3); err != nil {
			t.Fatal("replacement credentials unusable after restart", err)
		}
		f.mu.Lock()
		posts := f.posts
		f.mu.Unlock()
		if posts != 0 || attempts != 1 {
			t.Fatal("recovery replayed configuration POST", posts, attempts)
		}
	})
	t.Run("ConcurrentRefreshTokenABA", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.blockPath = "/assets"
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
		entered, release := f.entered, f.release
		f.mu.Unlock()
		result := make(chan error, 1)
		go func() { _, err := w.Refresh(t.Context(), l.ID, l.Revision, 3); result <- err }()
		<-entered
		renewed, err := other.Register(t.Context(), l.ID, l.Revision, appsBooksToken("new", "2040-01-01T00:00:00Z"))
		if err != nil {
			close(release)
			t.Fatal(err)
		}
		restored, err := other.Register(t.Context(), l.ID, renewed.Revision, token)
		if err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-result; !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal("ABA accepted stale refresh", err)
		}
		got, e := requireAppsBooksLocation(t, other, l.ID)
		if got.Revision != restored.Revision || len(e.Assets.Pages) != 0 {
			t.Fatal("stale evidence published", got, e)
		}
	})
	t.Run("ConcurrentConfigurationReservation", func(t *testing.T) {
		a, b := factory(t)
		_, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		// Reserve durably, then stop the POST at the transport boundary.
		entered, release := make(chan struct{}), make(chan struct{})
		transport := cfg.HTTPClient.Transport
		blocking := *cfg.HTTPClient
		blocking.Transport = appsBooksRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				close(entered)
				<-release
			}
			return transport.RoundTrip(r)
		})
		blockCfg := cfg
		blockCfg.HTTPClient = &blocking
		worker := newAppsBooksWorkflow(t, a, blockCfg)
		result := make(chan error, 1)
		go func() {
			_, err := worker.Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "stable"}})
			result <- err
		}()
		<-entered
		pending, _ := requireAppsBooksLocation(t, other, l.ID)
		if !pending.ConfigurationPending {
			close(release)
			t.Fatal("POST started without durable reservation")
		}
		if _, err := other.Register(t.Context(), l.ID, pending.Revision, token); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			close(release)
			t.Fatal(err)
		}
		if _, err := other.Configure(t.Context(), l.ID, pending.Revision, appsbooks.ClientConfigurationRequest{}); !errors.Is(err, appsbooks.ErrConfigurationPending) {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ConfigurationPreflightFencedByReplacement", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.blockPath = "/config"
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
		entered, release := f.entered, f.release
		f.mu.Unlock()
		result := make(chan error, 1)
		go func() {
			_, err := w.Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "stable"}})
			result <- err
		}()
		<-entered
		if _, err := other.Register(t.Context(), l.ID, l.Revision, appsBooksToken("replacement", "2040-01-01T00:00:00Z")); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-result; !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal(err)
		}
		f.mu.Lock()
		posts := f.posts
		f.mu.Unlock()
		if posts != 0 {
			t.Fatal("stale preflight sent configuration POST")
		}
	})
	t.Run("TokenValidationFencedByConfiguration", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		l, err := w.Register(t.Context(), "London", 0, token)
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.blockPath = "/config"
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
		entered, release := f.entered, f.release
		f.mu.Unlock()
		result := make(chan error, 1)
		go func() {
			_, err := w.Register(t.Context(), l.ID, l.Revision, appsBooksToken("replacement", "2040-01-01T00:00:00Z"))
			result <- err
		}()
		<-entered
		if _, err := other.Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "stable"}}); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-result; !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal(err)
		}
		got, _ := requireAppsBooksLocation(t, other, l.ID)
		if got.CredentialRevision != l.CredentialRevision || got.ConfigurationPending {
			t.Fatal("stale token validation changed credentials", got)
		}
	})
	t.Run("ConcurrentUIDClaim", func(t *testing.T) {
		a, b := factory(t)
		f, cfg, token := fixtureAppsBooks(t)
		w, other := newAppsBooksWorkflow(t, a, cfg), newAppsBooksWorkflow(t, b, cfg)
		for n := 0; n < 8; n++ {
			f.mu.Lock()
			f.uid = fmt.Sprintf("uid-%d", n)
			f.mu.Unlock()
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() { <-start; _, err := w.Register(t.Context(), fmt.Sprintf("A-%d", n), 0, token); results <- err }()
			go func() {
				<-start
				_, err := other.Register(t.Context(), fmt.Sprintf("B-%d", n), 0, token)
				results <- err
			}()
			close(start)
			first, second := <-results, <-results
			if !((first == nil && errors.Is(second, appsbooks.ErrLocation)) || (second == nil && errors.Is(first, appsbooks.ErrLocation))) {
				t.Fatalf("duplicate UID claim: %v / %v", first, second)
			}
		}
	})
}

type appsBooksRoundTripper func(*http.Request) (*http.Response, error)

// RoundTrip delegates to the deterministic fixture transport.
func (r appsBooksRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

// appsBooksClock advances rate-limit timers without wall-clock sleeps.
type appsBooksClock struct{ *clock.Fake }

// After delivers a timer immediately after advancing the fixture clock.
func (c *appsBooksClock) After(d time.Duration) <-chan time.Time {
	c.Advance(d)
	return c.Fake.After(0)
}
