package ddmtest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

func item(kind schemaddm.Kind, identifier, token string) ddm.SnapshotItem {
	return ddm.SnapshotItem{DeclarationRef: ddm.DeclarationRef{Kind: kind, Identifier: identifier, ServerToken: token}, BaseToken: token}
}

// RunSnapshotSuite covers SnapshotStore.
func RunSnapshotSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("PutReplacesItems", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		first := &ddm.Snapshot{ID: dev, DeclarationsToken: "t1", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{
			item(schemaddm.KindConfiguration, "a", "ta"),
			item(schemaddm.KindActivation, "b", "tb"),
		}}
		if err := s.PutSnapshot(ctx, first); err != nil {
			t.Fatal(err)
		}
		got, err := s.Snapshot(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != dev || got.DeclarationsToken != "t1" || len(got.Items) != 2 || got.Items[0].Identifier != "a" || got.Items[1].ServerToken != "tb" {
			t.Fatalf("first snapshot: %+v", got)
		}
		wantTime(t, "TokenChangedAt", got.TokenChangedAt, t0)
		wantTime(t, "RefreshedAt", got.RefreshedAt, t0)
		second := &ddm.Snapshot{ID: dev, DeclarationsToken: "t2", TokenChangedAt: t0.Add(time.Minute), RefreshedAt: t0.Add(2 * time.Minute), Items: []ddm.SnapshotItem{
			item(schemaddm.KindConfiguration, "c", "tc"),
		}}
		if err := s.PutSnapshot(ctx, second); err != nil {
			t.Fatal(err)
		}
		got, err = s.Snapshot(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if got.DeclarationsToken != "t2" || len(got.Items) != 1 || got.Items[0].Identifier != "c" {
			t.Fatalf("replaced snapshot: %+v", got)
		}
		wantTime(t, "TokenChangedAt", got.TokenChangedAt, t0.Add(time.Minute))
		wantTime(t, "RefreshedAt", got.RefreshedAt, t0.Add(2*time.Minute))
		// An empty manifest is a valid snapshot.
		if err := s.PutSnapshot(ctx, &ddm.Snapshot{ID: dev, DeclarationsToken: "t3", TokenChangedAt: t0, RefreshedAt: t0}); err != nil {
			t.Fatal(err)
		}
		got, _ = s.Snapshot(ctx, dev)
		if len(got.Items) != 0 {
			t.Fatalf("empty snapshot kept items: %+v", got.Items)
		}
	})

	t.Run("ExpandedBytesRoundTrip", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		expanded := []byte(`{"Identifier":"a","Payload":{"Name":"DEVICE-01"},"Type":"com.apple.configuration.test"}`)
		it := item(schemaddm.KindConfiguration, "a", "base")
		it.ServerToken = "expanded"
		it.Expanded = bytes.Clone(expanded)
		snap := &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{it, item(schemaddm.KindAsset, "b", "tb")}}
		if err := s.PutSnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
		// The store copied the input.
		snap.Items[0].Expanded[0] = '!'
		snap.Items[1].Identifier = "changed"
		got, err := s.Snapshot(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Items[0].Expanded, expanded) || got.Items[0].BaseToken != "base" || got.Items[0].ServerToken != "expanded" {
			t.Fatalf("expanded item: %+v", got.Items[0])
		}
		if len(got.Items[1].Expanded) != 0 || got.Items[1].Identifier != "b" {
			t.Fatalf("plain item: %+v", got.Items[1])
		}
		// The store returned a copy.
		got.Items[0].Expanded[0] = '!'
		got.Items = got.Items[:1]
		again, _ := s.Snapshot(ctx, dev)
		if !bytes.Equal(again.Items[0].Expanded, expanded) || len(again.Items) != 2 {
			t.Fatal("Snapshot returned aliased items")
		}
	})

	t.Run("UnknownNotFound", func(t *testing.T) {
		s := newStore(t)
		_, err := s.Snapshot(ctx, Device(1))
		wantErr(t, "unknown snapshot", err, ddm.ErrNotFound)
	})
}
