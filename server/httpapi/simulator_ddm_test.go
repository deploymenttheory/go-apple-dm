package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/inmem"
)

// Declaration types used by the tests.
const (
	typeActivation = "com.apple.activation.simple"
	typeConfig     = "com.apple.configuration.passcode.settings"
	typeAsset      = "com.apple.asset.data"
	typeProperties = "com.apple.management.properties"
	typeOrgInfo    = "com.apple.management.organization-info"
)

// stubDecl is one declaration the stub serves.
type stubDecl struct {
	Kind, Type, Identifier, ServerToken string
	Payload                             map[string]any
	// gone keeps the declaration in the manifest but answers 404.
	gone bool
}

// ddmStub is a canned DDM server behind service.Config.DeclarativeManagement.
// Tests mutate it between rounds.
type ddmStub struct {
	mu       sync.Mutex
	token    int
	decls    map[string]*stubDecl
	hits     map[string]int
	channels map[string][]mdm.Channel
	statuses []json.RawMessage
	// fail maps an endpoint to the HTTP status it answers with.
	fail map[string]int
	// badJSON makes an endpoint answer with a non-JSON body.
	badJSON map[string]bool
	// noToken makes "tokens" answer without a DeclarationsToken.
	noToken bool
	// statusBody makes "status" answer with a body.
	statusBody string
	// wrongIdentifier, when set, replaces the Identifier in declaration bodies.
	wrongIdentifier string
	// churn bumps the token after every tokens and declaration-items
	// answer, so the sync never settles (Fleet issue 43050).
	churn bool
	// bumpAfterTokens bumps the token after that many tokens answers, so the
	// manifest arrives with a newer token than the tokens call reported.
	bumpAfterTokens int
}

func newDDMStub() *ddmStub {
	return &ddmStub{token: 1, decls: map[string]*stubDecl{}, hits: map[string]int{}, channels: map[string][]mdm.Channel{}, fail: map[string]int{}, badJSON: map[string]bool{}}
}

func (s *ddmStub) tok() string { return fmt.Sprintf("tok-%d", s.token) }

// put adds or replaces a declaration and bumps the manifest token.
func (s *ddmStub) put(kind, typ, id, serverToken string, payload map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if payload == nil {
		payload = map[string]any{}
	}
	s.decls[kind+"/"+id] = &stubDecl{Kind: kind, Type: typ, Identifier: id, ServerToken: serverToken, Payload: payload}
	s.token++
}

func (s *ddmStub) remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.decls, key)
	s.token++
}

func (s *ddmStub) hit(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[endpoint]
}

func (s *ddmStub) declarationHits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for ep, c := range s.hits {
		if strings.HasPrefix(ep, "declaration/") {
			n += c
		}
	}
	return n
}

func (s *ddmStub) reports() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.statuses)
}

func jsonBody(v any) service.DMResponse {
	b, _ := json.Marshal(v)
	return service.DMResponse{Body: b, ContentType: "application/json"}
}

func (s *ddmStub) handle(_ context.Context, _ *mdm.Request, ck *mdm.Checkin, m *checkin.DeclarativeManagement) (service.DMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[m.Endpoint]++
	s.channels[m.Endpoint] = append(s.channels[m.Endpoint], ck.ID.Channel)
	if st := s.fail[m.Endpoint]; st != 0 {
		return service.DMResponse{Status: st}, nil
	}
	if s.badJSON[m.Endpoint] {
		return service.DMResponse{Body: []byte("{not json"), ContentType: "application/json"}, nil
	}
	switch m.Endpoint {
	case "tokens":
		sync := map[string]any{"DeclarationsToken": s.tok(), "Timestamp": "2026-09-02T00:00:00Z"}
		if s.noToken {
			delete(sync, "DeclarationsToken")
		}
		if s.churn {
			s.token++
		}
		if s.bumpAfterTokens > 0 {
			s.bumpAfterTokens--
			s.token++
		}
		return jsonBody(map[string]any{"SyncTokens": sync}), nil
	case "declaration-items":
		groups := map[string][]map[string]string{"Activations": {}, "Configurations": {}, "Assets": {}, "Management": {}}
		names := map[string]string{"activation": "Activations", "configuration": "Configurations", "asset": "Assets", "management": "Management"}
		for _, key := range slices.Sorted(maps.Keys(s.decls)) {
			d := s.decls[key]
			groups[names[d.Kind]] = append(groups[names[d.Kind]], map[string]string{"Identifier": d.Identifier, "ServerToken": d.ServerToken})
		}
		resp := jsonBody(map[string]any{"Declarations": groups, "DeclarationsToken": s.tok()})
		if s.churn {
			s.token++
		}
		return resp, nil
	case "status":
		s.statuses = append(s.statuses, json.RawMessage(slices.Clone(m.Data)))
		return service.DMResponse{Body: []byte(s.statusBody)}, nil
	}
	key := strings.TrimPrefix(m.Endpoint, "declaration/")
	d, ok := s.decls[key]
	if !ok || d.gone {
		return service.DMResponse{Status: http.StatusNotFound}, nil
	}
	body := map[string]any{"Type": d.Type, "Identifier": d.Identifier, "ServerToken": d.ServerToken, "Payload": d.Payload}
	if len(d.Payload) == 0 {
		delete(body, "Payload") // Apple always sends Payload; the client tolerates its absence
	}
	if s.wrongIdentifier != "" {
		body["Identifier"] = s.wrongIdentifier
	}
	return jsonBody(body), nil
}

// ddmHarness is a real service core and HTTP API with the stub behind the
// DeclarativeManagement check-in.
type ddmHarness struct {
	stub *ddmStub
	core *service.Core
	srv  *httptest.Server
}

func newDDMHarness(t *testing.T) *ddmHarness {
	t.Helper()
	h := &ddmHarness{stub: newDDMStub()}
	core, err := service.New(service.Config{Store: inmem.New(), Pinning: service.PinOff, DeclarativeManagement: h.stub.handle})
	if err != nil {
		t.Fatal(err)
	}
	h.core = core
	h.srv = httptest.NewServer(httpapi.Handler(httpapi.Config{Checkin: core, Connect: core}))
	t.Cleanup(h.srv.Close)
	return h
}

// device enrolls a fresh simulated device against the harness.
func (h *ddmHarness) device(t *testing.T, udid string, opts ...simulator.Option) *simulator.Device {
	t.Helper()
	d := simulator.New(udid, append([]simulator.Option{simulator.WithURLs(h.srv.URL+"/mdm", h.srv.URL+"/mdm"), simulator.WithClient(h.srv.Client())}, opts...)...)
	if err := d.Enroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

// seed installs the standard three declarations: a predicate-free
// activation, the configuration it references, and management properties.
func (s *ddmStub) seed() {
	s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}})
	s.put("configuration", typeConfig, "cfg", "c1", map[string]any{"MinimumLength": 6})
	s.put("management", typeProperties, "props", "p1", map[string]any{"shard": 10, "site": "hq"})
}

// row finds one declaration in a decoded report.
type reportRow struct {
	Identifier  string `json:"identifier"`
	ServerToken string `json:"server-token"`
	Active      bool   `json:"active"`
	Valid       string `json:"valid"`
	Reasons     []struct {
		Code        string         `json:"code"`
		Description *string        `json:"description"`
		Details     map[string]any `json:"details"`
	} `json:"reasons"`
}

type decodedReport struct {
	FullReport  *bool `json:"FullReport"`
	StatusItems struct {
		Device     map[string]map[string]any `json:"device"`
		Management struct {
			Declarations *struct {
				Activations    []reportRow `json:"activations"`
				Configurations []reportRow `json:"configurations"`
				Assets         []reportRow `json:"assets"`
				Management     []reportRow `json:"management"`
			} `json:"declarations"`
			ClientCapabilities *struct {
				SupportedVersions []string `json:"supported-versions"`
				SupportedPayloads struct {
					Declarations map[string][]string `json:"declarations"`
					StatusItems  []string            `json:"status-items"`
				} `json:"supported-payloads"`
			} `json:"client-capabilities"`
		} `json:"management"`
	} `json:"StatusItems"`
}

func decodeReport(t *testing.T, body []byte) decodedReport {
	t.Helper()
	var r decodedReport
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("report %s: %v", body, err)
	}
	return r
}

func (r decodedReport) rows() []reportRow {
	d := r.StatusItems.Management.Declarations
	if d == nil {
		return nil
	}
	return slices.Concat(d.Activations, d.Configurations, d.Assets, d.Management)
}

func (r decodedReport) row(t *testing.T, id string) reportRow {
	t.Helper()
	for _, row := range r.rows() {
		if row.Identifier == id {
			return row
		}
	}
	t.Fatalf("declaration %q not in report", id)
	return reportRow{}
}

func (r reportRow) codes() []string {
	out := make([]string, 0, len(r.Reasons))
	for _, reason := range r.Reasons {
		out = append(out, reason.Code)
	}
	return out
}

func TestSyncDDMFirstSyncFetchesEverything(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1", simulator.WithDDM(map[string]any{"site": "branch", "region": "eu"}))
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"activation/act", "configuration/cfg", "management/props"}
	if res.Rounds != 1 || !res.Changed || !slices.Equal(res.Fetched, want) || len(res.Removed) != 0 || res.Token != "tok-4" {
		t.Fatalf("result = %+v", res)
	}
	st := d.DDM()
	if st.DeclarationsToken != "tok-4" || st.Items == nil || len(st.Declarations) != 3 || st.LastSync.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	if got := st.Declarations["configuration/cfg"]; got.Type != typeConfig || got.ServerToken != "c1" || got.Payload["MinimumLength"] != float64(6) || got.Kind != "configuration" {
		t.Fatalf("configuration = %+v", got)
	}
	// Properties are merged only when a report is graded.
	_ = d.DDMStatusReport(true)
	st = d.DDM()
	if st.Properties["site"] != "hq" || st.Properties["region"] != "eu" || st.Properties["shard"] != float64(10) {
		t.Fatalf("properties = %v", st.Properties)
	}
	if h.stub.hit("tokens") != 1 || h.stub.hit("declaration-items") != 1 || h.stub.declarationHits() != 3 {
		t.Fatalf("hits = %v", h.stub.hits)
	}
}

func TestSyncDDMUnchangedTokenSkipsItems(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	if _, err := d.SyncDDM(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 || res.Changed || res.Fetched != nil || res.Removed != nil || res.Token != "tok-4" {
		t.Fatalf("result = %+v", res)
	}
	if h.stub.hit("tokens") != 2 || h.stub.hit("declaration-items") != 1 || h.stub.declarationHits() != 3 {
		t.Fatalf("hits = %v", h.stub.hits)
	}
}

func TestSyncDDMChangedTokenFetchesOnlyChanged(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	if _, err := d.SyncDDM(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.stub.put("configuration", typeConfig, "cfg", "c2", map[string]any{"MinimumLength": 8})
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 || !res.Changed || !slices.Equal(res.Fetched, []string{"configuration/cfg"}) || res.Token != "tok-5" {
		t.Fatalf("result = %+v", res)
	}
	if got := d.DDM().Declarations["configuration/cfg"]; got.ServerToken != "c2" || got.Payload["MinimumLength"] != float64(8) {
		t.Fatalf("configuration = %+v", got)
	}
	if h.stub.declarationHits() != 4 || h.stub.hit("declaration-items") != 2 {
		t.Fatalf("hits = %v", h.stub.hits)
	}
}

func TestSyncDDMRemovalByManifest(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	if _, err := d.SyncDDM(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.stub.remove("management/props")
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || len(res.Fetched) != 0 || !slices.Equal(res.Removed, []string{"management/props"}) {
		t.Fatalf("result = %+v", res)
	}
	if st := d.DDM(); len(st.Declarations) != 2 || st.Declarations["management/props"] != nil {
		t.Fatalf("state = %+v", st.Declarations)
	}
}

func TestSyncDDM404RemovesDeclaration(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	if _, err := d.SyncDDM(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The manifest still lists cfg with a new token, but the fetch is 404.
	h.stub.put("configuration", typeConfig, "cfg", "c9", nil)
	h.stub.mu.Lock()
	h.stub.decls["configuration/cfg"].gone = true
	h.stub.mu.Unlock()
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || len(res.Fetched) != 0 || !slices.Equal(res.Removed, []string{"configuration/cfg"}) {
		t.Fatalf("result = %+v", res)
	}
	if st := d.DDM(); st.Declarations["configuration/cfg"] != nil || len(st.Declarations) != 2 {
		t.Fatalf("state = %+v", st.Declarations)
	}
	// A 404 for a declaration never held is neither fetched nor removed.
	h.stub.mu.Lock()
	h.stub.token++
	h.stub.mu.Unlock()
	if res, err := d.SyncDDM(context.Background()); err != nil || len(res.Fetched) != 0 || len(res.Removed) != 0 || !res.Changed {
		t.Fatalf("second sync = %+v %v", res, err)
	}
}

func TestSyncDDMConvergesWithinRounds(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	h.stub.mu.Lock()
	h.stub.bumpAfterTokens = 1
	h.stub.mu.Unlock()
	d := h.device(t, "D1")
	res, err := d.SyncDDM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Round 1 sees tok-4 then a manifest at tok-5; round 2 confirms tok-5
	// from the tokens endpoint alone.
	if res.Rounds != 2 || res.Token != "tok-5" || len(res.Fetched) != 3 || h.stub.hit("tokens") != 2 || h.stub.hit("declaration-items") != 1 {
		t.Fatalf("result = %+v hits = %v", res, h.stub.hits)
	}
}

func TestSyncDDMNotSettled(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	h.stub.mu.Lock()
	h.stub.churn = true
	h.stub.mu.Unlock()
	d := h.device(t, "D1", simulator.WithDDMMaxRounds(3))
	res, err := d.SyncDDM(context.Background())
	if !errors.Is(err, simulator.ErrDDMNotSettled) || res.Rounds != 3 || h.stub.hit("tokens") != 3 {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	// The default bound applies when the option is zero.
	d2 := h.device(t, "D2", simulator.WithDDMMaxRounds(0))
	if res, err := d2.SyncDDM(context.Background()); !errors.Is(err, simulator.ErrDDMNotSettled) || res.Rounds != 5 {
		t.Fatalf("default bound: %+v %v", res, err)
	}
}

func TestSyncDDMBadJSON(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	ctx := context.Background()
	set := func(f func(s *ddmStub)) {
		h.stub.mu.Lock()
		defer h.stub.mu.Unlock()
		h.stub.badJSON = map[string]bool{}
		h.stub.noToken = false
		f(h.stub)
	}
	cases := []struct {
		name string
		f    func(s *ddmStub)
	}{
		{"tokens", func(s *ddmStub) { s.badJSON["tokens"] = true }},
		{"no token", func(s *ddmStub) { s.noToken = true }},
		{"items", func(s *ddmStub) { s.badJSON["declaration-items"] = true }},
		{"declaration", func(s *ddmStub) { s.badJSON["declaration/activation/act"] = true }},
	}
	for _, tc := range cases {
		set(tc.f)
		if _, err := d.SyncDDM(ctx); !errors.Is(err, simulator.ErrDDMBadResponse) {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
	}
	// A declaration whose identifier does not match the endpoint.
	set(func(s *ddmStub) { s.wrongIdentifier = "other" })
	if _, err := d.SyncDDM(ctx); !errors.Is(err, simulator.ErrDDMBadResponse) {
		t.Fatalf("identifier mismatch: %v", err)
	}
	// A manifest without a token.
	set(func(s *ddmStub) { s.wrongIdentifier = "" })
	items := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"SyncTokens":{"DeclarationsToken":"x"}}`
		if strings.Contains(readAll(r), "declaration-items") {
			body = `{"Declarations":{}}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(items.Close)
	if _, err := simulator.New("U", simulator.WithURLs(items.URL, items.URL)).SyncDDM(ctx); !errors.Is(err, simulator.ErrDDMBadResponse) {
		t.Fatalf("manifest without token: %v", err)
	}
	// Status endpoint answering with a body.
	set(func(s *ddmStub) { s.statusBody = "{}" })
	if err := d.PostDDMStatus(ctx, true); !errors.Is(err, simulator.ErrDDMBadResponse) {
		t.Fatalf("status body: %v", err)
	}
}

func readAll(r *http.Request) string {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String()
		}
	}
}

func TestSyncDDMServerError(t *testing.T) {
	t.Parallel()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1")
	ctx := context.Background()
	var he *simulator.HTTPError
	for _, ep := range []string{"tokens", "declaration-items", "declaration/configuration/cfg"} {
		h.stub.mu.Lock()
		h.stub.fail = map[string]int{ep: http.StatusInternalServerError}
		h.stub.mu.Unlock()
		if _, err := d.SyncDDM(ctx); !errors.As(err, &he) || he.Status != 500 {
			t.Fatalf("%s: err = %v", ep, err)
		}
		if st := d.DDM(); st.Items != nil || st.DeclarationsToken != "" || !st.LastSync.IsZero() {
			t.Fatalf("%s: token must not advance on failure: %+v", ep, st)
		}
	}
	h.stub.mu.Lock()
	h.stub.fail = map[string]int{"status": http.StatusBadRequest}
	h.stub.mu.Unlock()
	if _, err := d.SyncDDM(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.PostDDMStatus(ctx, true); !errors.As(err, &he) || he.Status != 400 {
		t.Fatalf("status: %v", err)
	}
	if !d.DDM().LastReport.IsZero() {
		t.Fatal("a rejected report must not count as posted")
	}
}

func TestStatusReportRules(t *testing.T) {
	t.Parallel()
	type want struct {
		active bool
		valid  string
		codes  []string
	}
	cases := []struct {
		name  string
		props map[string]any
		seed  func(s *ddmStub)
		want  map[string]want
		check func(t *testing.T, r decodedReport)
	}{
		{
			name:  "predicate true activates the chain",
			props: map[string]any{"shard": 10},
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "@property(shard) <= 50"})
				s.put("configuration", typeConfig, "cfg", "c1", map[string]any{"Asset": "blob"})
				s.put("asset", typeAsset, "blob", "s1", nil)
			},
			want: map[string]want{"act": {true, "valid", nil}, "cfg": {true, "valid", nil}, "blob": {true, "valid", nil}},
		},
		{
			name:  "predicate false",
			props: map[string]any{"shard": 80},
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "@property(shard) <= 50"})
				s.put("configuration", typeConfig, "cfg", "c1", nil)
			},
			want: map[string]want{"act": {false, "valid", []string{"Info.Predicate"}}, "cfg": {false, "valid", []string{"Error.ActivationFailed"}}},
			check: func(t *testing.T, r decodedReport) {
				t.Helper()
				act := r.row(t, "act").Reasons[0].Details
				if act["Identifier"] != "act" || act["ServerToken"] != "a1" || act["Predicate"] != "@property(shard) <= 50" {
					t.Fatalf("Info.Predicate details = %v", act)
				}
				cfg := r.row(t, "cfg").Reasons[0]
				if cfg.Details["Identifier"] != "act" || cfg.Details["ServerToken"] != "a1" || cfg.Description == nil {
					t.Fatalf("Error.ActivationFailed = %+v", cfg)
				}
			},
		},
		{
			name: "management properties override client properties",
			// The client thinks shard=80 but the server's properties say 10.
			props: map[string]any{"shard": 80},
			seed: func(s *ddmStub) {
				s.seed()
				s.put("activation", typeActivation, "act", "a2", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "@property(shard) <= 50 AND @property(site) == 'hq'"})
			},
			want: map[string]want{"act": {true, "valid", nil}, "cfg": {true, "valid", nil}, "props": {false, "valid", nil}},
		},
		{
			name: "configuration referenced by one active and one inactive activation",
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "off", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "FALSEPREDICATE"})
				s.put("activation", typeActivation, "on", "a2", map[string]any{"StandardConfigurations": []any{"cfg"}})
				s.put("configuration", typeConfig, "cfg", "c1", nil)
			},
			want: map[string]want{"off": {false, "valid", []string{"Info.Predicate"}}, "on": {true, "valid", nil}, "cfg": {true, "valid", nil}},
		},
		{
			name: "configuration referenced by no activation",
			seed: func(s *ddmStub) { s.put("configuration", typeConfig, "cfg", "c1", nil) },
			want: map[string]want{"cfg": {false, "valid", []string{"Info.NotReferencedByActivation"}}},
		},
		{
			name: "unknown configuration type",
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}})
				s.put("configuration", "com.example.configuration.future", "cfg", "c1", nil)
			},
			want: map[string]want{"act": {true, "valid", nil}, "cfg": {false, "invalid", []string{"Error.UnknownDeclarationType"}}},
			check: func(t *testing.T, r decodedReport) {
				t.Helper()
				if d := r.row(t, "cfg").Reasons[0].Details; d["UnknownDeclarationType"] != "com.example.configuration.future" {
					t.Fatalf("details = %v", d)
				}
			},
		},
		{
			name: "type known but of another kind",
			seed: func(s *ddmStub) { s.put("configuration", typeActivation, "cfg", "c1", nil) },
			want: map[string]want{"cfg": {false, "invalid", []string{"Error.UnknownDeclarationType"}}},
		},
		{
			name: "activation referencing a missing configuration",
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg", "nope"}})
				s.put("configuration", typeConfig, "cfg", "c1", nil)
			},
			want: map[string]want{"act": {false, "invalid", []string{"Error.MissingConfigurations"}}, "cfg": {false, "valid", []string{"Error.ActivationFailed"}}},
			check: func(t *testing.T, r decodedReport) {
				t.Helper()
				d := r.row(t, "act").Reasons[0].Details
				if ids, _ := d["ConfigurationIdentifiers"].([]any); len(ids) != 1 || ids[0] != "nope" || d["Identifier"] != "act" {
					t.Fatalf("details = %v", d)
				}
			},
		},
		{
			name: "unparsable predicate",
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "@property(shard) MATCHES '.*'"})
				s.put("configuration", typeConfig, "cfg", "c1", nil)
			},
			want: map[string]want{"act": {false, "invalid", []string{"Error.UnableToParsePredicate"}}, "cfg": {false, "valid", []string{"Error.ActivationFailed"}}},
		},
		{
			name:  "predicate that cannot be evaluated",
			props: map[string]any{"shard": 10},
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"Predicate": "@property(shard) < 'x'"})
			},
			want: map[string]want{"act": {false, "invalid", []string{"Error.UnableToEvaluatePredicate"}}},
		},
		{
			name: "unknown activation type is not graded further",
			seed: func(s *ddmStub) {
				s.put("activation", "com.example.activation.future", "act", "a1", map[string]any{"StandardConfigurations": []any{"cfg"}})
				s.put("configuration", typeConfig, "cfg", "c1", nil)
			},
			want: map[string]want{"act": {false, "invalid", []string{"Error.UnknownDeclarationType"}}, "cfg": {false, "valid", []string{"Info.NotReferencedByActivation"}}},
		},
		{
			name: "management declarations are never active",
			seed: func(s *ddmStub) {
				s.put("management", typeOrgInfo, "org", "m1", map[string]any{"Name": "Example"})
				s.put("management", "com.example.management.future", "odd", "m2", nil)
			},
			want: map[string]want{"org": {false, "valid", nil}, "odd": {false, "invalid", []string{"Error.UnknownDeclarationType"}}},
		},
		{
			name: "assets follow their configurations",
			seed: func(s *ddmStub) {
				s.put("activation", typeActivation, "act", "a1", map[string]any{"StandardConfigurations": []any{"on", "off"}})
				s.put("configuration", typeConfig, "on", "c1", map[string]any{"Refs": []any{map[string]any{"Asset": "used"}}})
				s.put("configuration", typeConfig, "off", "c2", nil)
				s.put("asset", typeAsset, "used", "s1", nil)
				s.put("asset", typeAsset, "orphan", "s2", nil)
				s.put("asset", "com.example.asset.future", "odd", "s3", nil)
			},
			want: map[string]want{
				"act": {true, "valid", nil}, "on": {true, "valid", nil}, "off": {true, "valid", nil},
				"used": {true, "valid", nil}, "orphan": {false, "valid", []string{"Info.NotReferencedByConfiguration"}}, "odd": {false, "invalid", []string{"Error.UnknownDeclarationType"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newDDMHarness(t)
			tc.seed(h.stub)
			d := h.device(t, "D1", simulator.WithDDM(tc.props))
			if _, err := d.SyncDDM(context.Background()); err != nil {
				t.Fatal(err)
			}
			r := decodeReport(t, d.DDMStatusReport(true))
			if r.FullReport == nil || !*r.FullReport {
				t.Fatal("FullReport missing")
			}
			for id, w := range tc.want {
				row := r.row(t, id)
				if row.Active != w.active || row.Valid != w.valid || !slices.Equal(row.codes(), w.codes) {
					t.Errorf("%s: active=%v valid=%s reasons=%v, want %+v", id, row.Active, row.Valid, row.codes(), w)
				}
			}
			if len(r.rows()) != len(tc.want) {
				t.Errorf("report has %d rows, want %d", len(r.rows()), len(tc.want))
			}
			if tc.check != nil {
				tc.check(t, r)
			}
			// The graded state is visible on the snapshot too.
			for _, decl := range d.DDM().Declarations {
				if w, ok := tc.want[decl.Identifier]; ok && (decl.Active != w.active || decl.Valid != w.valid) {
					t.Errorf("snapshot %s: active=%v valid=%s", decl.Identifier, decl.Active, decl.Valid)
				}
			}
		})
	}

	t.Run("device items and client capabilities", func(t *testing.T) {
		t.Parallel()
		h := newDDMHarness(t)
		d := h.device(t, "D1")
		d.SerialNumber, d.Model, d.ModelName, d.OSVersion, d.BuildVersion = "C02XYZ", "MacBookPro18,1", "MacBook Pro", "26.0", "26A100"
		r := decodeReport(t, d.DDMStatusReport(true))
		dev := r.StatusItems.Device
		if dev["identifier"]["serial-number"] != "C02XYZ" || dev["identifier"]["udid"] != "D1" || dev["model"]["family"] != "Mac" || dev["model"]["identifier"] != "MacBookPro18,1" || dev["model"]["marketing-name"] != "MacBook Pro" {
			t.Fatalf("device = %v", dev)
		}
		if os := dev["operating-system"]; os["family"] != "macOS" || os["marketing-name"] != "macOS" || os["version"] != "26.0" || os["build-version"] != "26A100" {
			t.Fatalf("operating-system = %v", os)
		}
		caps := r.StatusItems.Management.ClientCapabilities
		if caps == nil || !slices.Equal(caps.SupportedVersions, []string{"1.0.0"}) {
			t.Fatalf("capabilities = %+v", caps)
		}
		decls := caps.SupportedPayloads.Declarations
		if !slices.Contains(decls["activations"], typeActivation) || !slices.Contains(decls["configurations"], typeConfig) || !slices.Contains(decls["assets"], typeAsset) || !slices.Contains(decls["management"], typeProperties) {
			t.Fatalf("declarations = %v", decls)
		}
		if slices.Contains(decls["configurations"], "com.apple.credential.acme") || !slices.IsSorted(decls["configurations"]) {
			t.Fatalf("configurations = %v", decls["configurations"])
		}
		items := caps.SupportedPayloads.StatusItems
		if !slices.Contains(items, "management.declarations") || !slices.Contains(items, "device.identifier.udid") || slices.ContainsFunc(items, func(s string) bool { return strings.HasPrefix(s, "test.") }) {
			t.Fatalf("status items = %v", items)
		}
		if len(r.rows()) != 0 || r.StatusItems.Management.Declarations == nil {
			t.Fatalf("empty full report should carry an empty declarations item: %+v", r.StatusItems.Management.Declarations)
		}
		// Test items on request.
		d2 := h.device(t, "D2", simulator.WithDDMTestStatusItems(true))
		if items := decodeReport(t, d2.DDMStatusReport(true)).StatusItems.Management.ClientCapabilities.SupportedPayloads.StatusItems; !slices.Contains(items, "test.string-value") {
			t.Fatalf("test items missing: %v", items)
		}
		// Other families.
		for model, want := range map[string][2]string{"iPhone17,1": {"iPhone", "iOS"}, "iPad16,3": {"iPad", "iPadOS"}, "AppleTV14,1": {"Apple TV", "tvOS"}, "Watch7,1": {"Apple Watch", "watchOS"}, "RealityDevice14,1": {"Apple Vision", "visionOS"}, "Mac16,1": {"Mac", "macOS"}} {
			dm := h.device(t, "D-"+model)
			dm.Model = model
			dev := decodeReport(t, dm.DDMStatusReport(true)).StatusItems.Device
			if dev["model"]["family"] != want[0] || dev["operating-system"]["family"] != want[1] {
				t.Errorf("%s: family = %v %v", model, dev["model"]["family"], dev["operating-system"]["family"])
			}
		}
	})
}

func TestStatusReportIncremental(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newDDMHarness(t)
	h.stub.seed()
	h.stub.put("activation", typeActivation, "gated", "g1", map[string]any{"StandardConfigurations": []any{"cfg"}, "Predicate": "@property(region) == 'eu'"})
	d := h.device(t, "D1", simulator.WithDDM(map[string]any{"region": "eu"}))
	if _, err := d.SyncDDM(ctx); err != nil {
		t.Fatal(err)
	}
	// Before anything is posted an incremental report carries the device
	// items, the capabilities, and every declaration, but is not "full".
	first := decodeReport(t, d.DDMStatusReport(false))
	if first.FullReport != nil || first.StatusItems.Management.ClientCapabilities == nil || first.StatusItems.Device == nil || len(first.rows()) != 4 {
		t.Fatalf("first incremental = %s", d.DDMStatusReport(false))
	}
	if err := d.PostDDMStatus(ctx, false); err != nil {
		t.Fatal(err)
	}
	if d.DDM().LastReport.IsZero() || len(h.stub.reports()) != 1 {
		t.Fatal("post not recorded")
	}
	// Nothing changed: no declarations, no capabilities, no device items.
	quiet := decodeReport(t, d.DDMStatusReport(false))
	if quiet.StatusItems.Management.Declarations != nil || quiet.StatusItems.Management.ClientCapabilities != nil || quiet.StatusItems.Device != nil {
		t.Fatalf("quiet incremental = %s", d.DDMStatusReport(false))
	}
	// Flip the property: the gated activation and nothing else changes
	// grade (cfg stays active through "act").
	simulator.WithDDM(map[string]any{"region": "us"})(d)
	inc := decodeReport(t, d.DDMStatusReport(false))
	if rows := inc.rows(); len(rows) != 1 || rows[0].Identifier != "gated" || rows[0].Active || !slices.Equal(rows[0].codes(), []string{"Info.Predicate"}) || inc.StatusItems.Device != nil {
		t.Fatalf("incremental after flip = %s", d.DDMStatusReport(false))
	}
	// A full report carries everything regardless.
	full := decodeReport(t, d.DDMStatusReport(true))
	if full.FullReport == nil || len(full.rows()) != 4 || full.StatusItems.Device == nil || full.StatusItems.Management.ClientCapabilities == nil {
		t.Fatalf("full = %s", d.DDMStatusReport(true))
	}
	// Posting the incremental report moves the baseline; a removed
	// declaration simply drops out of later reports.
	if err := d.PostDDMStatus(ctx, false); err != nil {
		t.Fatal(err)
	}
	h.stub.remove("activation/gated")
	if _, err := d.SyncDDM(ctx); err != nil {
		t.Fatal(err)
	}
	after := decodeReport(t, d.DDMStatusReport(false))
	if after.StatusItems.Management.Declarations != nil {
		t.Fatalf("removal should not be reported: %s", d.DDMStatusReport(false))
	}
	if rows := decodeReport(t, d.DDMStatusReport(true)).rows(); len(rows) != 3 {
		t.Fatalf("full after removal has %d rows", len(rows))
	}
	if reports := h.stub.reports(); len(reports) != 2 || !strings.Contains(string(reports[1]), `"gated"`) || strings.Contains(string(reports[1]), "client-capabilities") {
		t.Fatalf("posted reports = %s", reports)
	}
}

func TestUserChannelDDM(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1", simulator.WithDDM(map[string]any{"region": "eu"}))
	u := d.User("U1", "alice", "Alice")
	if _, err := u.Authenticate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := u.TokenUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := u.SyncDDM(ctx)
	if err != nil || len(res.Fetched) != 3 {
		t.Fatalf("user sync = %+v %v", res, err)
	}
	if st := u.DDM(); len(st.Declarations) != 3 || st.DeclarationsToken != "tok-4" {
		t.Fatalf("user state = %+v", st)
	}
	if st := d.DDM(); len(st.Declarations) != 0 || st.Items != nil {
		t.Fatal("device channel state must stay separate")
	}
	r := decodeReport(t, u.DDMStatusReport(true))
	if !r.row(t, "act").Active || r.StatusItems.Device["identifier"]["udid"] != "D1" {
		t.Fatalf("user report = %s", u.DDMStatusReport(true))
	}
	if err := u.PostDDMStatus(ctx, true); err != nil {
		t.Fatal(err)
	}
	h.stub.mu.Lock()
	defer h.stub.mu.Unlock()
	for _, ep := range []string{"tokens", "declaration-items", "declaration/activation/act", "status"} {
		if chans := h.stub.channels[ep]; len(chans) != 1 || chans[0] != mdm.ChannelUser {
			t.Fatalf("%s seen on %v, want the user channel", ep, chans)
		}
	}
	if len(h.stub.statuses) != 1 || !strings.Contains(string(h.stub.statuses[0]), `"FullReport":true`) {
		t.Fatalf("statuses = %s", h.stub.statuses)
	}
}

func TestDDMFaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("DropStatus", func(t *testing.T) {
		t.Parallel()
		h := newDDMHarness(t)
		h.stub.seed()
		d := h.device(t, "D1", simulator.WithDDMFaults(simulator.DDMFaults{DropStatus: true}))
		if _, err := d.SyncDDM(ctx); err != nil {
			t.Fatal(err)
		}
		if err := d.PostDDMStatus(ctx, true); err != nil {
			t.Fatal(err)
		}
		if h.stub.hit("status") != 0 || !d.DDM().LastReport.IsZero() {
			t.Fatal("dropped report must not be posted or recorded")
		}
		// The report is still graded.
		if !d.DDM().Declarations["activation/act"].Active {
			t.Fatal("grading skipped")
		}
	})

	t.Run("StaleToken", func(t *testing.T) {
		t.Parallel()
		h := newDDMHarness(t)
		h.stub.seed()
		d := h.device(t, "D1")
		if _, err := d.SyncDDM(ctx); err != nil {
			t.Fatal(err)
		}
		simulator.WithDDMFaults(simulator.DDMFaults{StaleToken: true})(d)
		res, err := d.SyncDDM(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Fetched) != 3 || !res.Changed || h.stub.hit("declaration-items") != 2 || h.stub.declarationHits() != 6 {
			t.Fatalf("stale sync = %+v hits = %v", res, h.stub.hits)
		}
	})

	t.Run("FailFetch", func(t *testing.T) {
		t.Parallel()
		h := newDDMHarness(t)
		h.stub.seed()
		d := h.device(t, "D1", simulator.WithDDMFaults(simulator.DDMFaults{FailFetch: true}))
		if _, err := d.SyncDDM(ctx); !errors.Is(err, simulator.ErrDDMFault) {
			t.Fatalf("first sync: %v", err)
		}
		if h.stub.declarationHits() != 0 || len(d.DDM().Declarations) != 0 {
			t.Fatal("fault must fire before the request and leave state alone")
		}
		if res, err := d.SyncDDM(ctx); err != nil || len(res.Fetched) != 3 {
			t.Fatalf("second sync = %+v %v", res, err)
		}
	})
}

func TestConnectRunsSyncOnDeclarativeManagement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newDDMHarness(t)
	h.stub.seed()
	d := h.device(t, "D1", simulator.WithDDM(map[string]any{"region": "eu"}))
	id := []mdm.EnrollmentID{{Channel: mdm.ChannelDevice, ID: "D1"}}
	enqueue := func(uuid string, data []byte) {
		t.Helper()
		cmd, err := mdm.NewCommand(&commands.DeclarativeManagement{Data: data}, mdm.WithUUID(uuid))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Enqueue(ctx, id, cmd, storage.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	lastReply := func() simulator.Reply { r := d.Replies(); return r[len(r)-1] }

	// First command: full sync and a full report before Acknowledged.
	enqueue("DM1", []byte(`{"SyncTokens":{"DeclarationsToken":"tok-4","Timestamp":"2026-09-02T00:00:00Z"}}`))
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 1 || got[0].UUID != "DM1" {
		t.Fatalf("Connect = %v %v", got, err)
	}
	if lastReply().Status != mdm.StatusAcknowledged {
		t.Fatalf("reply = %+v", lastReply())
	}
	st := d.DDM()
	if st.TokenHint != "tok-4" || len(st.Declarations) != 3 || st.LastReport.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	reports := h.stub.reports()
	if len(reports) != 1 || !strings.Contains(string(reports[0]), `"FullReport":true`) || !strings.Contains(string(reports[0]), `"client-capabilities"`) {
		t.Fatalf("reports = %s", reports)
	}

	// Second command without Data: nothing changed, incremental report.
	enqueue("DM2", nil)
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	reports = h.stub.reports()
	if len(reports) != 2 || strings.Contains(string(reports[1]), "FullReport") || strings.Contains(string(reports[1]), `"declarations"`) {
		t.Fatalf("second report = %s", reports[1])
	}
	if d.DDM().TokenHint != "tok-4" || h.stub.hit("declaration-items") != 1 {
		t.Fatal("second command should not refetch the manifest")
	}

	// Malformed Data is an Error reply without a sync.
	enqueue("DM3", []byte(`{"SyncTokens":`))
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if r := lastReply(); r.Status != mdm.StatusError || len(r.ErrorChain) != 1 || r.ErrorChain[0].ErrorDomain != "GoAppleMDMSimulatorDDMErrorDomain" || !strings.Contains(r.ErrorChain[0].LocalizedDescription, "command Data") {
		t.Fatalf("reply = %+v", lastReply())
	}
	if h.stub.hit("tokens") != 2 {
		t.Fatal("malformed Data must not trigger a sync")
	}

	// A failing server turns the reply into an Error with the cause.
	h.stub.mu.Lock()
	h.stub.fail["tokens"] = http.StatusInternalServerError
	h.stub.mu.Unlock()
	enqueue("DM4", nil)
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if r := lastReply(); r.Status != mdm.StatusError || !strings.Contains(r.ErrorChain[0].USEnglishDescription, "500") {
		t.Fatalf("reply = %+v", lastReply())
	}
	h.stub.mu.Lock()
	delete(h.stub.fail, "tokens")
	h.stub.fail["status"] = http.StatusBadRequest
	h.stub.mu.Unlock()
	enqueue("DM5", nil)
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if r := lastReply(); r.Status != mdm.StatusError || !strings.Contains(r.ErrorChain[0].USEnglishDescription, "400") {
		t.Fatalf("reply = %+v", lastReply())
	}

	// A Responder that does not acknowledge skips the client entirely.
	h.stub.mu.Lock()
	delete(h.stub.fail, "status")
	h.stub.mu.Unlock()
	before := h.stub.hit("tokens")
	d.Responder = func(*mdm.Command) simulator.Reply { return simulator.Reply{Status: mdm.StatusNotNow} }
	enqueue("DM6", nil)
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if lastReply().Status != mdm.StatusNotNow || h.stub.hit("tokens") != before {
		t.Fatalf("NotNow should skip the sync: %+v hits=%v", lastReply(), h.stub.hits)
	}
}
