package ddm_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/inmem"
)

// subscriptionNames lists the StatusItems names of a served
// management.status-subscriptions declaration.
func subscriptionNames(t *testing.T, body []byte) []string {
	t.Helper()
	m := decode(t, body)
	if m["Type"] != schemaddm.DeclarationTypeManagementStatusSubscriptions || m["Identifier"] != ddm.SubscriptionIdentifier {
		t.Fatalf("not a subscriptions declaration: %s", body)
	}
	items, _ := m["Payload"].(map[string]any)["StatusItems"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["Name"].(string))
	}
	return out
}

// subscribed serves the synthesised declaration for id.
func subscribed(t *testing.T, h *harness, id mdm.EnrollmentID) ([]string, []byte) {
	t.Helper()
	body, err := h.engine.Declaration(context.Background(), id, schemaddm.KindConfiguration, ddm.SubscriptionIdentifier)
	if err != nil {
		t.Fatalf("Declaration(subscriptions): %v", err)
	}
	return subscriptionNames(t, body), body
}

func capabilitiesReport(t *testing.T, statusItems any) []byte {
	t.Helper()
	return report(t, boolp(true), map[string]any{"management": map[string]any{"client-capabilities": map[string]any{
		"supported-versions": []string{"1.0.0"},
		"supported-payloads": map[string]any{"status-items": statusItems},
	}}}, nil)
}

func enabled(c *ddm.Config) { c.Subscriptions = ddm.Subscriptions{Enabled: true} }

func TestSubscriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("Disabled", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range snap.Items {
			if it.Identifier == ddm.SubscriptionIdentifier {
				t.Fatalf("subscriptions synthesised while disabled: %+v", snap.Items)
			}
		}
		if _, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, ddm.SubscriptionIdentifier); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("served while disabled: %v", err)
		}
	})
	t.Run("BaselineWhenUnknown", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, enabled)
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Items) != 1 || snap.Items[0].Identifier != ddm.SubscriptionIdentifier || snap.Items[0].Kind != schemaddm.KindConfiguration {
			t.Fatalf("manifest %+v", snap.Items)
		}
		names, body := subscribed(t, h, dev)
		if !slices.Equal(names, ddm.DefaultSubscriptionBaseline) || len(names) != 11 {
			t.Fatalf("baseline %v", names)
		}
		// The ServerToken is TokenFor of the canonical declaration, so it
		// round-trips through ParseDeclaration.
		parsed, err := ddm.ParseDeclaration(body, support.Target{})
		if err != nil || parsed.ServerToken != snap.Items[0].ServerToken || decode(t, body)["ServerToken"] != parsed.ServerToken {
			t.Fatalf("token %+v %v vs %+v", parsed, err, snap.Items[0])
		}
		// A custom baseline replaces Apple's.
		custom := newHarness(t, func(c *ddm.Config) {
			c.Subscriptions = ddm.Subscriptions{Enabled: true, Baseline: []string{"device.model.family"}}
		})
		if names, _ := subscribed(t, custom, dev); strings.Join(names, ",") != "device.model.family" {
			t.Fatalf("custom baseline %v", names)
		}
	})
	t.Run("ConvergesAfterCapabilitiesReport", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, enabled)
		before, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		reported := []string{"management.declarations", "device.model.family", "management.client-capabilities", "device.model.family"}
		if _, err := h.engine.Status(ctx, dev, capabilitiesReport(t, reported)); err != nil {
			t.Fatal(err)
		}
		after, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if after.DeclarationsToken == before.DeclarationsToken {
			t.Fatal("manifest token did not change after the capabilities report")
		}
		names, _ := subscribed(t, h, dev)
		if strings.Join(names, ",") != "device.model.family,management.client-capabilities,management.declarations" {
			t.Fatalf("subscriptions after report %v", names)
		}
		if _, err := h.engine.Status(ctx, dev, capabilitiesReport(t, reported)); err != nil {
			t.Fatal(err)
		}
		again, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if again.DeclarationsToken != after.DeclarationsToken || again.TokenChangedAt != after.TokenChangedAt {
			t.Fatalf("manifest moved after an identical report: %+v vs %+v", again, after)
		}
		// Another enrollment is unaffected and still on the baseline.
		if names, _ := subscribed(t, h, ddmtest.Device(2)); len(names) != 11 {
			t.Fatalf("other enrollment %v", names)
		}
	})
	t.Run("DefensiveAgainstBadCapabilities", func(t *testing.T) {
		t.Parallel()
		for name, item := range map[string]any{
			"String":         "nope",
			"Array":          []any{"device.model.family"},
			"ItemsString":    map[string]any{"supported-payloads": map[string]any{"status-items": "device.model.family"}},
			"ItemsNumbers":   map[string]any{"supported-payloads": map[string]any{"status-items": []any{1, 2}}},
			"PayloadsString": map[string]any{"supported-payloads": "nope"},
			"EmptyItems":     map[string]any{"supported-payloads": map[string]any{"status-items": []any{"", ""}}},
			"OnlyExcluded":   map[string]any{"supported-payloads": map[string]any{"status-items": []any{"test.array"}}},
		} {
			h := newHarness(t, enabled)
			body := report(t, boolp(true), map[string]any{"management": map[string]any{"client-capabilities": item}}, nil)
			if _, err := h.engine.Status(ctx, dev, body); err != nil {
				t.Fatalf("%s: store: %v", name, err)
			}
			names, _ := subscribed(t, h, dev)
			if !slices.Equal(names, ddm.DefaultSubscriptionBaseline) {
				t.Errorf("%s: %v, want the baseline", name, names)
			}
		}
	})
	t.Run("ExcludesTestItems", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, enabled)
		if _, err := h.engine.Status(ctx, dev, capabilitiesReport(t, []string{"test.array", "device.model.family", "test.dictionary"})); err != nil {
			t.Fatal(err)
		}
		if names, _ := subscribed(t, h, dev); strings.Join(names, ",") != "device.model.family" {
			t.Fatalf("subscriptions %v", names)
		}
		custom := newHarness(t, func(c *ddm.Config) {
			c.Subscriptions = ddm.Subscriptions{Enabled: true, Exclude: []string{"device."}}
		})
		if _, err := custom.engine.Status(ctx, dev, capabilitiesReport(t, []string{"test.array", "device.model.family"})); err != nil {
			t.Fatal(err)
		}
		if names, _ := subscribed(t, custom, dev); strings.Join(names, ",") != "test.array" {
			t.Fatalf("custom exclude %v", names)
		}
		none := newHarness(t, func(c *ddm.Config) {
			c.Subscriptions = ddm.Subscriptions{Enabled: true, Exclude: []string{}}
		})
		if _, err := none.engine.Status(ctx, dev, capabilitiesReport(t, []string{"test.array"})); err != nil {
			t.Fatal(err)
		}
		if names, _ := subscribed(t, none, dev); strings.Join(names, ",") != "test.array" {
			t.Fatalf("empty exclude %v", names)
		}
	})
	t.Run("AppearsInManifestAndItems", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, enabled)
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		// Sorted by identifier: com.deploymenttheory sorts before com.example.
		if len(snap.Items) != 2 || snap.Items[0].Identifier != ddm.SubscriptionIdentifier || snap.Items[1].Identifier != "com.example.cfg" {
			t.Fatalf("manifest %+v", snap.Items)
		}
		sub := snap.Items[0]
		got, _ := items(t, h, dev)
		if len(got.Declarations.Configurations) != 2 || got.Declarations.Configurations[0] != (itemRef{ddm.SubscriptionIdentifier, sub.ServerToken}) {
			t.Fatalf("items %+v", got.Declarations.Configurations)
		}
		tok, _ := tokens(t, h, dev)
		if tok.SyncTokens.DeclarationsToken != snap.DeclarationsToken || got.DeclarationsToken != snap.DeclarationsToken {
			t.Fatal("tokens disagree with the manifest")
		}
		_, body := subscribed(t, h, dev)
		if decode(t, body)["ServerToken"] != sub.ServerToken {
			t.Fatalf("served token differs from the manifest: %s", body)
		}
	})
	t.Run("AdminDeclarationWins", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, enabled)
		admin := h.put(declJSON(schemaddm.DeclarationTypeManagementStatusSubscriptions, ddm.SubscriptionIdentifier,
			map[string]any{"StatusItems": []map[string]any{{"Name": "device.model.family"}}}))
		h.assign(dev, ddm.SubscriptionIdentifier)
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Items) != 1 || snap.Items[0].ServerToken != admin.ServerToken || snap.Items[0].Expanded != nil {
			t.Fatalf("manifest %+v", snap.Items)
		}
		names, body := subscribed(t, h, dev)
		if strings.Join(names, ",") != "device.model.family" || decode(t, body)["ServerToken"] != admin.ServerToken {
			t.Fatalf("served %s", body)
		}
		// A resolver naming the admin declaration takes precedence too.
		resolved := newHarness(t, func(c *ddm.Config) {
			enabled(c)
			c.Resolvers = []ddm.Resolver{resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) {
				return []string{ddm.SubscriptionIdentifier}, nil
			})}
		})
		resolved.put(declJSON(schemaddm.DeclarationTypeManagementStatusSubscriptions, ddm.SubscriptionIdentifier,
			map[string]any{"StatusItems": []map[string]any{{"Name": "device.model.identifier"}}}))
		if names, _ := subscribed(t, resolved, dev); strings.Join(names, ",") != "device.model.identifier" {
			t.Fatalf("resolver-supplied admin declaration lost: %v", names)
		}
		// Once the admin declaration goes, the synthesised one returns.
		if err := h.engine.DeleteDeclaration(ctx, ddm.SubscriptionIdentifier); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if names, _ := subscribed(t, h, dev); len(names) != 11 {
			t.Fatalf("after delete %v", names)
		}
	})
	t.Run("StoreFailureUsesBaseline", func(t *testing.T) {
		t.Parallel()
		// A capabilities read that fails for a reason other than "never
		// reported" is logged and the baseline used, so a device is never
		// refused a manifest for it.
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{}}
		h := newHarness(t, func(c *ddm.Config) {
			enabled(c)
			c.Store = failing
		})
		if _, err := h.engine.Status(ctx, dev, capabilitiesReport(t, []string{"device.model.family"})); err != nil {
			t.Fatal(err)
		}
		if names, _ := subscribed(t, h, dev); strings.Join(names, ",") != "device.model.family" {
			t.Fatalf("subscriptions %v", names)
		}
		failing.Fail["StatusValues"] = errBoom
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatalf("Manifest with unreadable capabilities: %v", err)
		}
		if names, _ := subscribed(t, h, dev); !slices.Equal(names, ddm.DefaultSubscriptionBaseline) {
			t.Fatalf("subscriptions %v, want the baseline", names)
		}
		if !strings.Contains(h.logs.String(), "client capabilities unreadable") {
			t.Fatalf("failure not logged: %s", h.logs.String())
		}
	})
}
