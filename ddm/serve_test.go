package ddm_test

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

// tokensResponse is the SyncTokens body as decoded from the wire.
type tokensResponse struct {
	SyncTokens struct {
		DeclarationsToken string `json:"DeclarationsToken"`
		Timestamp         string `json:"Timestamp"`
	} `json:"SyncTokens"`
}

// itemsResponse is the DeclarationItemsResponse as decoded from the wire.
type itemsResponse struct {
	Declarations struct {
		Activations    []itemRef `json:"Activations"`
		Assets         []itemRef `json:"Assets"`
		Configurations []itemRef `json:"Configurations"`
		Management     []itemRef `json:"Management"`
	} `json:"Declarations"`
	DeclarationsToken string `json:"DeclarationsToken"`
}

type itemRef struct {
	Identifier  string `json:"Identifier"`
	ServerToken string `json:"ServerToken"`
}

func tokens(t *testing.T, h *harness, id mdm.EnrollmentID) (tokensResponse, []byte) {
	t.Helper()
	body, err := h.engine.Tokens(context.Background(), id)
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	var out tokensResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out, body
}

func items(t *testing.T, h *harness, id mdm.EnrollmentID) (itemsResponse, []byte) {
	t.Helper()
	body, err := h.engine.DeclarationItems(context.Background(), id)
	if err != nil {
		t.Fatalf("DeclarationItems: %v", err)
	}
	var out itemsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out, body
}

func TestTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("ByteStableWhileUnchanged", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		_, first := tokens(t, h, dev)
		h.clock.Advance(time.Hour)
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		h.clock.Advance(time.Hour)
		if _, err := h.engine.DeclarationItems(ctx, dev); err != nil {
			t.Fatal(err)
		}
		// A no-op re-upload does not disturb the response either.
		h.put(configTest("com.example.cfg", "hi"))
		_, again := tokens(t, h, dev)
		if string(first) != string(again) {
			t.Fatalf("tokens response moved without a change:\n%s\n%s", first, again)
		}
	})
	t.Run("TimestampAdvancesOnlyWhenTokenChanges", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		first, _ := tokens(t, h, dev)
		if first.SyncTokens.Timestamp != "2026-09-01T12:00:00Z" {
			t.Fatalf("Timestamp %q", first.SyncTokens.Timestamp)
		}
		h.clock.Advance(time.Hour)
		same, _ := tokens(t, h, dev)
		if same != first {
			t.Fatalf("response changed without a token change: %+v", same)
		}
		h.clock.Advance(time.Hour)
		h.put(configTest("com.example.cfg", "changed"))
		changed, _ := tokens(t, h, dev)
		if changed.SyncTokens.DeclarationsToken == first.SyncTokens.DeclarationsToken {
			t.Fatal("token did not change")
		}
		if changed.SyncTokens.Timestamp != "2026-09-01T14:00:00Z" {
			t.Fatalf("Timestamp %q after the change", changed.SyncTokens.Timestamp)
		}
		h.clock.Advance(time.Hour)
		if later, _ := tokens(t, h, dev); later != changed {
			t.Fatalf("Timestamp moved without a token change: %+v", later)
		}
		snap, err := h.store.Snapshot(ctx, dev)
		if err != nil || snap.TokenChangedAt != t0.Add(2*time.Hour) || snap.RefreshedAt != t0.Add(3*time.Hour) {
			t.Fatalf("snapshot times %+v, %v", snap, err)
		}
	})
	t.Run("EmptyManifest", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		got, _ := tokens(t, h, ddmtest.Device(9))
		if got.SyncTokens.DeclarationsToken != ddm.DeclarationsToken(nil) {
			t.Fatalf("empty manifest token %q", got.SyncTokens.DeclarationsToken)
		}
		if _, err := h.engine.Tokens(ctx, mdm.EnrollmentID{}); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})
	t.Run("Format", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) { c.Clock = nil })
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		got, body := tokens(t, h, dev)
		if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(got.SyncTokens.Timestamp) {
			t.Fatalf("Timestamp %q", got.SyncTokens.Timestamp)
		}
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got.SyncTokens.DeclarationsToken) {
			t.Fatalf("DeclarationsToken %q", got.SyncTokens.DeclarationsToken)
		}
		want := `{"SyncTokens":{"DeclarationsToken":"` + got.SyncTokens.DeclarationsToken + `","Timestamp":"` + got.SyncTokens.Timestamp + `"}}`
		if string(body) != want {
			t.Fatalf("body %s is not canonical", body)
		}
		rendered, err := ddm.RenderTokens("tok", time.Date(2026, 1, 2, 3, 4, 5, 999, time.FixedZone("x", 3600)))
		if err != nil || string(rendered) != `{"SyncTokens":{"DeclarationsToken":"tok","Timestamp":"2026-01-02T02:04:05Z"}}` {
			t.Fatalf("RenderTokens = %s, %v", rendered, err)
		}
	})
}

func TestDeclarationItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("AllFourArraysPresent", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		body, err := h.engine.DeclarationItems(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		decls, ok := m["Declarations"].(map[string]any)
		if !ok {
			t.Fatalf("Declarations missing: %s", body)
		}
		for _, key := range []string{"Activations", "Assets", "Configurations", "Management"} {
			arr, ok := decls[key].([]any)
			if !ok || len(arr) != 0 {
				t.Errorf("%s = %#v, want an empty array", key, decls[key])
			}
		}
		if len(decls) != 4 {
			t.Fatalf("Declarations has %d keys: %s", len(decls), body)
		}
		if m["DeclarationsToken"] != ddm.DeclarationsToken(nil) {
			t.Fatalf("token %v", m["DeclarationsToken"])
		}
		want := `{"Declarations":{"Activations":[],"Assets":[],"Configurations":[],"Management":[]},"DeclarationsToken":"` + ddm.DeclarationsToken(nil) + `"}`
		if string(body) != want {
			t.Fatalf("body %s", body)
		}
	})
	t.Run("GroupedByKind", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		act := h.put(activation("com.example.act", "com.example.cfg"))
		cfg := h.put(configTest("com.example.cfg", "hi"))
		asset := h.put(assetData("com.example.asset", "https://example.com/x"))
		props := h.put(properties("com.example.props", map[string]any{"a": 1}))
		h.assign(dev, "com.example.act", "com.example.cfg", "com.example.asset", "com.example.props")
		got, _ := items(t, h, dev)
		d := got.Declarations
		if len(d.Activations) != 1 || d.Activations[0] != (itemRef{act.Identifier, act.ServerToken}) {
			t.Errorf("Activations = %+v", d.Activations)
		}
		if len(d.Configurations) != 1 || d.Configurations[0] != (itemRef{cfg.Identifier, cfg.ServerToken}) {
			t.Errorf("Configurations = %+v", d.Configurations)
		}
		if len(d.Assets) != 1 || d.Assets[0] != (itemRef{asset.Identifier, asset.ServerToken}) {
			t.Errorf("Assets = %+v", d.Assets)
		}
		if len(d.Management) != 1 || d.Management[0] != (itemRef{props.Identifier, props.ServerToken}) {
			t.Errorf("Management = %+v", d.Management)
		}
		snap, err := h.store.Snapshot(ctx, dev)
		if err != nil || got.DeclarationsToken != snap.DeclarationsToken {
			t.Fatalf("items token %s, snapshot %+v %v", got.DeclarationsToken, snap, err)
		}
		tok, _ := tokens(t, h, dev)
		if tok.SyncTokens.DeclarationsToken != got.DeclarationsToken {
			t.Fatal("tokens and items disagree")
		}
	})
	t.Run("Sorted", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for _, id := range []string{"com.example.c", "com.example.a", "com.example.b"} {
			h.put(configTest(id, id))
			h.assign(dev, id)
		}
		got, _ := items(t, h, dev)
		ids := make([]string, 0, 3)
		for _, ref := range got.Declarations.Configurations {
			ids = append(ids, ref.Identifier)
		}
		if strings.Join(ids, ",") != "com.example.a,com.example.b,com.example.c" {
			t.Fatalf("order %v", ids)
		}
		unsorted := &ddm.Snapshot{Items: []ddm.SnapshotItem{
			{DeclarationRef: ddm.DeclarationRef{Kind: schemaddm.KindManagement, Identifier: "z", ServerToken: "1"}},
			{DeclarationRef: ddm.DeclarationRef{Kind: schemaddm.KindActivation, Identifier: "b", ServerToken: "1"}},
			{DeclarationRef: ddm.DeclarationRef{Kind: schemaddm.KindActivation, Identifier: "a", ServerToken: "1"}},
		}}
		body, err := ddm.RenderDeclarationItems(unsorted)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"Activations":[{"Identifier":"a","ServerToken":"1"},{"Identifier":"b","ServerToken":"1"}]`) {
			t.Fatalf("rendered %s", body)
		}
		if unsorted.Items[0].Kind != schemaddm.KindManagement {
			t.Fatal("render mutated the snapshot")
		}
		if _, err := ddm.RenderDeclarationItems(&ddm.Snapshot{Items: []ddm.SnapshotItem{{DeclarationRef: ddm.DeclarationRef{Kind: "credential", Identifier: "x"}}}}); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("unknown kind: %v", err)
		}
	})
}

func TestDeclaration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("ServesCurrent", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, body)
		if got["ServerToken"] != snap.Items[0].ServerToken || got["Identifier"] != "com.example.cfg" || got["Type"] != schemaddm.DeclarationTypeManagementTest {
			t.Fatalf("served %s", body)
		}
		if got["Payload"].(map[string]any)["Echo"] != "hi" {
			t.Fatalf("payload %s", body)
		}
		// The body round-trips through ParseDeclaration to the same token:
		// the ServerToken it carries is derived from its own content.
		parsed, err := ddm.ParseDeclaration(body, support.Target{})
		if err != nil || parsed.ServerToken != got["ServerToken"] {
			t.Fatalf("served body does not round-trip: %+v %v", parsed, err)
		}
	})
	t.Run("ServesSnapshotVersionAfterReupload", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		old := h.put(configTest("com.example.cfg", "old"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		fresh := h.put(configTest("com.example.cfg", "new"))
		if fresh.ServerToken == old.ServerToken {
			t.Fatal("re-upload kept the token")
		}
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, body)
		if got["ServerToken"] != old.ServerToken || got["Payload"].(map[string]any)["Echo"] != "old" {
			t.Fatalf("served %s, want the advertised version %s", body, old.ServerToken)
		}
		// The next manifest refresh moves the snapshot to the new version.
		if _, err := h.engine.Tokens(ctx, dev); err != nil {
			t.Fatal(err)
		}
		body, err = h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		if got := decode(t, body); got["ServerToken"] != fresh.ServerToken || got["Payload"].(map[string]any)["Echo"] != "new" {
			t.Fatalf("served %s after refresh, want %s", body, fresh.ServerToken)
		}
	})
	t.Run("ExpandedTokenMatchesManifest", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Expander = expanderFunc(func(_ context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
				return []byte(strings.Replace(string(d.Canonical), "$ID", id.ID, 1)), nil
			})
		})
		h.put(configTest("com.example.cfg", "$ID"))
		h.assign(dev, "com.example.cfg")
		got, _ := items(t, h, dev)
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		served := decode(t, body)
		if served["ServerToken"] != got.Declarations.Configurations[0].ServerToken {
			t.Fatalf("served token %v, advertised %v", served["ServerToken"], got.Declarations.Configurations[0].ServerToken)
		}
		if served["Payload"].(map[string]any)["Echo"] != dev.ID {
			t.Fatalf("served %s", body)
		}
	})
	t.Run("UnknownIs404", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.put(configTest("com.example.unassigned", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.unassigned"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("unassigned: %v", err)
		}
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.ghost"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("unknown: %v", err)
		}
		if _, err := h.engine.Declaration(ctx, ddmtest.Device(2), schemaddm.KindConfiguration, "com.example.cfg"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("other enrollment: %v", err)
		}
		if _, err := h.engine.Declaration(ctx, mdm.EnrollmentID{}, schemaddm.KindConfiguration, "com.example.cfg"); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})
	t.Run("WrongKindIs404", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindActivation, "com.example.cfg"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("wrong kind: %v", err)
		}
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg"); err != nil {
			t.Fatalf("right kind: %v", err)
		}
	})
	t.Run("DeletedIs404", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if err := h.engine.DeleteDeclaration(ctx, "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("deleted: %v", err)
		}
		got, _ := items(t, h, dev)
		if len(got.Declarations.Configurations) != 0 {
			t.Fatalf("items still list the deleted declaration: %+v", got)
		}
	})
	t.Run("NoSnapshotComputesOne", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		d := h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.store.Snapshot(ctx, dev); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("snapshot before any fetch: %v", err)
		}
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		if got := decode(t, body); got["ServerToken"] != d.ServerToken {
			t.Fatalf("served %s", body)
		}
		snap, err := h.store.Snapshot(ctx, dev)
		if err != nil || len(snap.Items) != 1 {
			t.Fatalf("snapshot after fetch: %+v %v", snap, err)
		}
	})
}

func TestHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("AllEndpoints", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		d := h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		res, err := h.engine.Handle(ctx, dev, "tokens", nil)
		if err != nil || res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"SyncTokens"`) {
			t.Fatalf("tokens = %+v, %v", res, err)
		}
		res, err = h.engine.Handle(ctx, dev, "declaration-items", nil)
		if err != nil || res.Status != http.StatusOK || !strings.Contains(string(res.Body), d.ServerToken) {
			t.Fatalf("declaration-items = %+v, %v", res, err)
		}
		res, err = h.engine.Handle(ctx, dev, "declaration/configuration/com.example.cfg", nil)
		if err != nil || res.Status != http.StatusOK || decode(t, res.Body)["ServerToken"] != d.ServerToken {
			t.Fatalf("declaration = %+v, %v", res, err)
		}
		res, err = h.engine.Handle(ctx, dev, "status", []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}}}`))
		if err != nil || res.Status != http.StatusOK || len(res.Body) != 0 {
			t.Fatalf("status = %+v, %v", res, err)
		}
	})
	t.Run("BadEndpoint", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for _, ep := range []string{"", "nope", "declaration/credential/x", "declaration/configuration/"} {
			if _, err := h.engine.Handle(ctx, dev, ep, nil); !errors.Is(err, ddm.ErrBadEndpoint) {
				t.Errorf("%q: %v", ep, err)
			}
		}
	})
	t.Run("StatusNeedsData", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if _, err := h.engine.Handle(ctx, dev, "status", nil); !errors.Is(err, ddm.ErrBadEndpoint) {
			t.Fatalf("status without data: %v", err)
		}
		if _, err := h.engine.Handle(ctx, dev, "status", []byte("nope")); !errors.Is(err, ddm.ErrStatusMalformed) {
			t.Fatalf("malformed status: %v", err)
		}
	})
	t.Run("UnknownDeclaration404", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		res, err := h.engine.Handle(ctx, dev, "declaration/configuration/com.example.ghost", nil)
		if err != nil || res.Status != http.StatusNotFound || len(res.Body) != 0 {
			t.Fatalf("unknown declaration = %+v, %v", res, err)
		}
		// Other failures still surface as errors.
		if _, err := h.engine.Handle(ctx, mdm.EnrollmentID{}, "declaration/configuration/x", nil); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
		if _, err := h.engine.Handle(ctx, mdm.EnrollmentID{}, "tokens", nil); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id for tokens: %v", err)
		}
		if _, err := h.engine.Handle(ctx, mdm.EnrollmentID{}, "declaration-items", nil); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id for items: %v", err)
		}
	})
	t.Run("StatusOK", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		body := []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}},"FullReport":true}`)
		res, err := h.engine.Handle(ctx, dev, "status", body)
		if err != nil || res.Status != http.StatusOK || len(res.Body) != 0 {
			t.Fatalf("status = %+v, %v", res, err)
		}
		reports, err := h.engine.StatusReports(ctx, dev, paging.Page{})
		if err != nil || len(reports.Items) != 1 || string(reports.Items[0].Raw) != string(body) || !reports.Items[0].FullReport {
			t.Fatalf("StatusReports = %+v, %v", reports, err)
		}
	})
}
