package ddm_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestMixedFleetCompatibility(t *testing.T) {
	version := "26.6.2"
	var lookupErr error
	h := newHarness(t, func(c *ddm.Config) {
		c.Subscriptions.Enabled = true
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			target := support.Target{OS: support.MacOS, Channel: support.ChannelDevice, Supervised: true}
			if version != "" {
				target.Version = support.MustVersion(version)
			}
			return target, lookupErr
		}
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "mixed"}
	put := func(raw []byte) {
		d, _, err := h.engine.PutDeclaration(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
			t.Fatal(err)
		}
	}
	put(declJSON("com.apple.configuration.softwareupdate.settings", "updates", map[string]any{"Deferrals": map[string]any{"MajorPeriodInDays": 30}}))
	put(declJSON("com.apple.configuration.app.settings", "apps", map[string]any{}))
	put(activation("activation", "updates", "apps"))
	put(declJSON("com.apple.configuration.legacy", "legacy", map[string]any{"ProfileAssetReference": "profile-data"}))
	put(assetData("profile-data", "https://example.test/profile.mobileconfig"))
	put(declJSON("com.apple.configuration.legacy", "missing", map[string]any{"ProfileAssetReference": "absent"}))
	report, err := h.engine.Compatibility(t.Context(), id)
	if err != nil || len(report.Issues) != 4 {
		t.Fatalf("preview: %+v %v", report, err)
	}
	if _, err := h.store.Snapshot(t.Context(), id); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("preview persisted a snapshot: %v", err)
	}
	snap, err := h.engine.Manifest(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	checkActivation := func(want int) {
		t.Helper()
		body, err := h.engine.Declaration(t.Context(), id, schema.KindActivation, "activation")
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Payload     struct{ StandardConfigurations []string }
			ServerToken string
		}
		if err := json.Unmarshal(body, &env); err != nil || len(env.Payload.StandardConfigurations) != want {
			t.Fatalf("activation: %s %v", body, err)
		}
		for _, item := range snap.Items {
			if item.Identifier == "activation" && item.ServerToken != env.ServerToken {
				t.Fatal("served token differs from manifest")
			}
		}
	}
	checkActivation(1)
	if _, err := h.engine.Declaration(t.Context(), id, schema.KindConfiguration, "apps"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("OS27 declaration delivered to OS26", err)
	}
	version = "27.0"
	snap, err = h.engine.Manifest(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	checkActivation(2)
	if _, err := h.engine.Declaration(t.Context(), id, schema.KindConfiguration, "legacy"); err != nil {
		t.Fatal(err)
	}
	// Eligibility must also be checked against an already advertised snapshot.
	version = "26.6.2"
	if _, err := h.engine.Declaration(t.Context(), id, schema.KindConfiguration, "apps"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("stale snapshot escaped target validation", err)
	}
	before, err := h.store.Snapshot(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	lookupErr = errBoom
	if _, err := h.engine.Manifest(t.Context(), id); !errors.Is(err, errBoom) {
		t.Fatal("lookup error hidden", err)
	}
	after, err := h.store.Snapshot(t.Context(), id)
	if err != nil || after.DeclarationsToken != before.DeclarationsToken {
		t.Fatal("failed lookup changed manifest", err)
	}
	lookupErr, version = nil, ""
	unknown, err := h.engine.Manifest(t.Context(), id)
	if err != nil || len(unknown.Items) != 2 {
		t.Fatalf("unknown inventory must retain only bootstrap subscriptions: %+v %v", unknown, err)
	}
}

func TestCompatibilityDeletionKeepsIndependentDeclarations(t *testing.T) {
	h := newHarness(t, func(c *ddm.Config) {
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			return support.Target{OS: support.MacOS, Version: support.V(27, 0, 0), Supervised: true}, nil
		}
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "deleted-asset"}
	for _, raw := range [][]byte{assetData("asset", "https://example.test/profile"), declJSON("com.apple.configuration.legacy", "legacy", map[string]any{"ProfileAssetReference": "asset"}), declJSON("com.apple.configuration.softwareupdate.settings", "updates", map[string]any{})} {
		d, _, err := h.engine.PutDeclaration(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.engine.Manifest(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.DeleteDeclaration(t.Context(), "asset"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.Declaration(t.Context(), id, schema.KindConfiguration, "updates"); err != nil {
		t.Fatalf("unrelated setting removed with asset: %v", err)
	}
	if _, err := h.engine.Declaration(t.Context(), id, schema.KindConfiguration, "legacy"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("missing dependency delivered: %v", err)
	}
}
