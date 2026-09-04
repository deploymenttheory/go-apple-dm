package ddm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/ddmtest"
)

// goldenToken reads the DeclarationsToken pinned in testdata/golden_token.txt.
// Every backend must produce this value for the manifest goldenManifest
// builds (decision record 0019, claim 4).
func goldenToken(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden_token.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestDeclarationsToken(t *testing.T) {
	t.Parallel()
	a := ddm.DeclarationRef{Kind: schemaddm.KindConfiguration, Identifier: "com.example.a", ServerToken: "t1"}
	b := ddm.DeclarationRef{Kind: schemaddm.KindActivation, Identifier: "com.example.b", ServerToken: "t2"}
	t.Run("Length64", func(t *testing.T) {
		if got := ddm.DeclarationsToken(nil); len(got) != 64 {
			t.Fatalf("empty manifest token %q", got)
		}
		if got := ddm.DeclarationsToken([]ddm.DeclarationRef{a, b}); len(got) != 64 {
			t.Fatalf("token %q", got)
		}
	})
	t.Run("OrderIndependent", func(t *testing.T) {
		if ddm.DeclarationsToken([]ddm.DeclarationRef{a, b}) != ddm.DeclarationsToken([]ddm.DeclarationRef{b, a}) {
			t.Fatal("token depends on input order")
		}
	})
	t.Run("ContentSensitive", func(t *testing.T) {
		changed := a
		changed.ServerToken = "t9"
		if ddm.DeclarationsToken([]ddm.DeclarationRef{a, b}) == ddm.DeclarationsToken([]ddm.DeclarationRef{changed, b}) {
			t.Fatal("token ignores a server token change")
		}
		if ddm.DeclarationsToken([]ddm.DeclarationRef{a}) == ddm.DeclarationsToken([]ddm.DeclarationRef{a, b}) {
			t.Fatal("token ignores membership")
		}
	})
	t.Run("LengthPrefixed", func(t *testing.T) {
		x := []ddm.DeclarationRef{{Kind: "k", Identifier: "ab", ServerToken: "c"}}
		y := []ddm.DeclarationRef{{Kind: "k", Identifier: "a", ServerToken: "bc"}}
		if ddm.DeclarationsToken(x) == ddm.DeclarationsToken(y) {
			t.Fatal("concatenation ambiguity")
		}
	})
	t.Run("GoldenAcrossBackends", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		refs := goldenManifest(t, h)
		want := goldenToken(t)
		if len(want) != 64 {
			t.Fatalf("golden file holds %q", want)
		}
		if got := ddm.DeclarationsToken(refs); got != want {
			t.Fatalf("DeclarationsToken = %s, golden %s", got, want)
		}
		// The refs are what any backend hands back: kind, identifier, and the
		// content-derived ServerToken, so the golden pins the whole chain.
		snap, err := h.engine.Manifest(context.Background(), ddmtest.Device(1))
		if err != nil {
			t.Fatal(err)
		}
		if snap.DeclarationsToken != want {
			t.Fatalf("inmem manifest token = %s, golden %s", snap.DeclarationsToken, want)
		}
	})
}

func TestTokenFor(t *testing.T) {
	t.Parallel()
	target := support.Target{}
	got := ddm.TokenFor([]byte(`{"Identifier":"a","Payload":{},"Type":"t"}`))
	if len(got) != 64 {
		t.Fatalf("token %q", got)
	}
	if got == ddm.TokenFor([]byte(`{"Identifier":"b","Payload":{},"Type":"t"}`)) {
		t.Fatal("token ignores content")
	}
	t.Run("KeyOrderAndWhitespaceIgnored", func(t *testing.T) {
		t.Parallel()
		a, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":{"Echo":"hi","ReturnStatus":"Failed"}}`), target)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ddm.ParseDeclaration([]byte("{\n  \"Payload\" : {\"ReturnStatus\":\"Failed\", \"Echo\" : \"hi\"},\n  \"Identifier\" : \"a\",\n  \"Type\":\"com.apple.configuration.management.test\"\n}\n"), target)
		if err != nil {
			t.Fatal(err)
		}
		if a.ServerToken != b.ServerToken {
			t.Fatalf("%s != %s", a.ServerToken, b.ServerToken)
		}
		if string(a.Canonical) != string(b.Canonical) {
			t.Fatalf("canonical forms differ: %s vs %s", a.Canonical, b.Canonical)
		}
	})
	t.Run("Length64", func(t *testing.T) {
		t.Parallel()
		d, err := ddm.ParseDeclaration(configTest("a", "hi"), target)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.ServerToken) != 64 || strings.Trim(d.ServerToken, "0123456789abcdef") != "" {
			t.Fatalf("token %q", d.ServerToken)
		}
		if len(d.ServerToken) > ddm.MaxIdentifierBytes {
			t.Fatalf("token exceeds Apple's %d-octet guidance", ddm.MaxIdentifierBytes)
		}
	})
	t.Run("IndependentOfTime", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		first := newHarness(t)
		second := newHarness(t, func(c *ddm.Config) { c.Clock = clock.NewFake(t0.Add(365 * 24 * time.Hour)) })
		a := first.put(configTest("a", "hi"))
		first.clock.Advance(time.Hour)
		if err := first.engine.DeleteDeclaration(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := first.engine.GetDeclaration(ctx, "a"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatal("declaration survived delete")
		}
		again := first.put(configTest("a", "hi"))
		b := second.put(configTest("a", "hi"))
		if a.ServerToken != b.ServerToken || a.ServerToken != again.ServerToken {
			t.Fatalf("tokens differ by clock: %s %s %s", a.ServerToken, b.ServerToken, again.ServerToken)
		}
		if again.CreatedAt != t0.Add(time.Hour) {
			t.Fatalf("re-created declaration keeps the old CreatedAt: %v", again.CreatedAt)
		}
	})
}

func TestSortRefs(t *testing.T) {
	t.Parallel()
	refs := []ddm.DeclarationRef{
		{Kind: schemaddm.KindManagement, Identifier: "z"},
		{Kind: schemaddm.KindActivation, Identifier: "b"},
		{Kind: schemaddm.KindActivation, Identifier: "a", ServerToken: "2"},
		{Kind: schemaddm.KindActivation, Identifier: "a", ServerToken: "1"},
	}
	sorted := ddm.SortRefs(refs)
	if sorted[0].Identifier != "a" || sorted[0].ServerToken != "1" || sorted[1].ServerToken != "2" || sorted[2].Identifier != "b" || sorted[3].Kind != schemaddm.KindManagement {
		t.Fatalf("sorted = %+v", sorted)
	}
	if refs[0].Kind != schemaddm.KindManagement {
		t.Fatal("SortRefs mutated its input")
	}
}

func TestParseEndpoint(t *testing.T) {
	t.Parallel()
	good := map[string]ddm.Endpoint{
		"tokens":                            {Op: ddm.OpTokens},
		"declaration-items":                 {Op: ddm.OpDeclarationItems},
		"status":                            {Op: ddm.OpStatus},
		"declaration/configuration/com.a.b": {Op: ddm.OpDeclaration, Kind: schemaddm.KindConfiguration, Identifier: "com.a.b"},
		"declaration/activation/x":          {Op: ddm.OpDeclaration, Kind: schemaddm.KindActivation, Identifier: "x"},
		"declaration/asset/x":               {Op: ddm.OpDeclaration, Kind: schemaddm.KindAsset, Identifier: "x"},
		"declaration/management/x":          {Op: ddm.OpDeclaration, Kind: schemaddm.KindManagement, Identifier: "x"},
	}
	for in, want := range good {
		got, err := ddm.ParseEndpoint(in)
		if err != nil || got != want {
			t.Errorf("%q = %+v %v, want %+v", in, got, err, want)
		}
		if got.String() != in {
			t.Errorf("%q round trip = %q", in, got.String())
		}
	}
	bad := []string{"", "Tokens", "declaration", "declaration/", "declaration/configuration", "declaration/configuration/",
		"declaration/credential/x", "declaration/configuration/a/b", "declarations/configuration/x", "status/", "declaration/configuration/" + strings.Repeat("x", 65)}
	for _, in := range bad {
		if _, err := ddm.ParseEndpoint(in); !errors.Is(err, ddm.ErrBadEndpoint) {
			t.Errorf("%q: %v", in, err)
		}
	}
	if ddm.Op(0).String() != "unknown" {
		t.Fatal("zero op string")
	}
	if _, err := ddm.ParseKind("base"); !errors.Is(err, ddm.ErrInvalid) {
		t.Fatalf("base kind: %v", err)
	}
}
