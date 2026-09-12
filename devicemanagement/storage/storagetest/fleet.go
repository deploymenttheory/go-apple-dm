package storagetest

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

func runFleetUpgrade(t *testing.T, newStore Factory) {
	t.Helper()
	s := newStore(t)
	id := device(1)
	enroll(t, s, id, 1)
	ctx := t.Context()
	clearer, ok := s.(storage.CommandClearer)
	if !ok {
		t.Fatal("store does not implement exact command clearing")
	}
	if _, err := clearer.ClearCommand(ctx, id, ""); err == nil {
		t.Fatal("empty UUID accepted")
	}
	inventory, err := mdm.NewCommand(
		&commands.DeviceInformation{Queries: []string{"OSVersion", "BuildVersion", "ProductName"}},
		mdm.WithUUID("inventory"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []*mdm.Command{inventory, cmd(t, "clear-only-this"), cmd(t, "keep-this")} {
		if _, err := s.Enqueue(
			ctx,
			[]mdm.EnrollmentID{id},
			command,
			storage.EnqueueOptions{},
		); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := plist.Marshal(
		map[string]any{
			"UDID":        id.ID,
			"CommandUUID": "inventory",
			"Status":      "Acknowledged",
			"QueryResponses": map[string]any{
				"OSVersion":    "27.0",
				"BuildVersion": "new",
				"ProductName":  "Mac16,1",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := mdm.DecodeResponse(raw, "DeviceInformation")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreResult(ctx, id, resp, t0); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, id)
	if err != nil || e.Device.OSVersion != "27.0" || e.Device.ProductName != "Mac16,1" ||
		e.Device.BuildVersion != "new" {
		t.Fatalf("inventory not persisted: %+v %v", e, err)
	}
	if n, err := s.Clear(
		ctx,
		id,
		storage.ClearFilter{CommandUUID: "clear-only-this", RequestType: "DeviceInformation"},
	); err != nil ||
		n != 0 {
		t.Fatalf("intersecting filters: %d %v", n, err)
	}
	if n, err := clearer.ClearCommand(ctx, id, "clear-only-this"); err != nil || n != 1 {
		t.Fatalf("exact clear: %d %v", n, err)
	}
	if n, err := s.Clear(
		ctx,
		id,
		storage.ClearFilter{CommandUUID: "clear-only-this"},
	); err != nil ||
		n != 0 {
		t.Fatalf("idempotence: %d %v", n, err)
	}
	rows, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows.Items {
		want := storage.StatePending
		switch row.Command.UUID {
		case "inventory":
			want = storage.StateAcknowledged
		case "clear-only-this":
			want = storage.StateCleared
		}
		if row.State != want {
			t.Fatalf("%s: %s want %s", row.Command.UUID, row.State, want)
		}
	}
	if next, err := s.Next(
		ctx,
		id,
		false,
		t0,
	); err != nil || next == nil ||
		next.UUID != "keep-this" {
		t.Fatalf("remaining queue: %+v %v", next, err)
	}
}
