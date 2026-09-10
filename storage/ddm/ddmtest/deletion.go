package ddmtest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

type deletionExpander struct{}

func (deletionExpander) Expand(
	_ context.Context,
	_ mdm.EnrollmentID,
	d *ddm.Declaration,
) ([]byte, error) {
	return bytes.ReplaceAll(
		d.Canonical,
		[]byte("placeholder"),
		[]byte("private-expanded-value"),
	), nil
}

// RunExpandedDeletionSuite covers deletion and legacy snapshots across backends.
func RunExpandedDeletionSuite(t *testing.T, factory Factory) {
	t.Helper()
	ctx := t.Context()
	store := factory(t)
	config := ddm.Config{
		Store:         store,
		Expander:      deletionExpander{},
		Subscriptions: ddm.Subscriptions{Enabled: true},
	}
	engine, err := ddm.New(config)
	if err != nil {
		t.Fatal(err)
	}
	dev := Device(1)
	for _, identifier := range []string{"audit.deleted", ddm.SubscriptionIdentifier} {
		d, _, err := engine.PutDeclaration(
			ctx,
			[]byte(
				`{"Type":"com.apple.configuration.management.test","Identifier":"`+identifier+`","Payload":{"Echo":"placeholder"}}`,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.AssignDeclaration(ctx, dev, d.Identifier); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if _, _, err := engine.PutDeclaration(
			ctx,
			[]byte(
				`{"Type":"com.apple.configuration.management.test","Identifier":"`+identifier+`","Payload":{"Echo":"updated"}}`,
			),
		); err != nil {
			t.Fatal(err)
		}
		body, err := engine.Declaration(ctx, dev, schemaddm.KindConfiguration, identifier)
		if err != nil || !bytes.Contains(body, []byte("private-expanded-value")) {
			t.Fatalf("advertised version lost: %s %v", body, err)
		}
		if err := engine.DeleteDeclaration(ctx, identifier); err != nil {
			t.Fatal(err)
		}
		// Reopening the engine also exercises persisted snapshots.
		engine, err = ddm.New(config)
		if err != nil {
			t.Fatal(err)
		}
		body, err = engine.Declaration(ctx, dev, schemaddm.KindConfiguration, identifier)
		if identifier == ddm.SubscriptionIdentifier {
			if err != nil || bytes.Contains(body, []byte("private-expanded-value")) {
				t.Fatalf("deleted override served: %s %v", body, err)
			}
		} else if !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("deleted expansion served: %s %v", body, err)
		}
	}
	snapshot, err := engine.Manifest(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Items {
		if snapshot.Items[i].Identifier == ddm.SubscriptionIdentifier {
			snapshot.Items[i].BaseToken = snapshot.Items[i].ServerToken
		}
	}
	if err := store.PutSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Declaration(
		ctx,
		dev,
		schemaddm.KindConfiguration,
		ddm.SubscriptionIdentifier,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.Snapshot(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snapshot.Items {
		if item.Identifier == ddm.SubscriptionIdentifier && item.BaseToken != "" {
			t.Fatal("legacy snapshot was not refreshed")
		}
	}
}
