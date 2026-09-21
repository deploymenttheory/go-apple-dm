package ddmsync_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	mdminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// TestNewTokenGenerationReachesQueue checks new token generation reaches queue.
func TestNewTokenGenerationReachesQueue(t *testing.T) {
	for _, state := range []string{"pending", "sent", "NotNow"} {
		t.Run(state, func(t *testing.T) { checkNewTokenGeneration(t, state) })
	}
}

// checkNewTokenGeneration checks that a changed token generation survives push failure and becomes
// a follow-up command after acknowledgment.
func checkNewTokenGeneration(t *testing.T, state string) {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Now())
	ds := ddminmem.New()
	ms := mdminmem.New()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "review-device"}
	core, err := service.New(service.Config{Store: ms, ValidateTargets: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.ImportEnrollment(ctx, storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	eng, err := ddm.New(ddm.Config{Store: ds, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	pusher := &fakePusher{}
	n, err := ddmsync.NewNotifier(ddmsync.NotifierConfig{Store: ds, Tokens: eng, Enqueuer: ms, Pusher: pusher, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.PutDeclaration(ctx, []byte(`{"Type":"com.apple.management.properties","Identifier":"review.properties","Payload":{"a":1}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AssignDeclaration(ctx, id, "review.properties"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(3 * time.Second)
	if _, err := n.DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var firstUUID string
	if state != "pending" {
		first, err := ms.Next(ctx, id, false, clk.Now())
		if err != nil || first == nil {
			t.Fatal(err)
		}
		firstUUID = first.UUID
		if state == "NotNow" {
			if err := ms.StoreResult(ctx, id, &mdm.Response{CommandUUID: firstUUID, Status: mdm.StatusNotNow}, clk.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The device applied version A, but the command acknowledgement has not arrived.
	applied, err := eng.Tokens(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.PutDeclaration(ctx, []byte(`{"Type":"com.apple.management.properties","Identifier":"review.properties","Payload":{"a":2}}`)); err != nil {
		t.Fatal(err)
	}
	clk.Advance(3 * time.Second)
	pusher.err = errors.New("push unavailable after enqueue")
	res, err := n.DrainOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 1 || res.Failed != 1 {
		t.Fatalf("new generation not retained after push failure: %+v", res)
	}
	pusher.err = nil
	clk.Advance(storage.NotNowBackoff(1))
	res, err = n.DrainOnce(ctx)
	if err != nil || res.Queued != 0 || res.Deduped != 1 || res.Failed != 0 {
		t.Fatalf("same generation retry not suppressed: %+v %v", res, err)
	}
	current, err := eng.Tokens(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := ms.Next(ctx, id, false, clk.Now())
	if err != nil || retry == nil {
		t.Fatal(err)
	}
	if state == "pending" {
		firstUUID = retry.UUID
	}
	cmd, err := mdm.DecodeCommand(retry.Raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := cmd.Payload.(*commands.DeclarativeManagement)
	if !ok {
		t.Fatalf("unexpected command: %T", cmd.Payload)
	}
	data := payload.Data
	pending, err := ds.PendingChanges(ctx, clk.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("deduped=%d, pending changes=%d, command equals applied token=%v, command equals current token=%v", res.Deduped, len(pending), bytes.Equal(data, applied), bytes.Equal(data, current))
	if len(pending) != 0 {
		t.Fatalf("new generation not queued: %+v", res)
	}
	if err := ms.StoreResult(ctx, id, &mdm.Response{CommandUUID: firstUUID, Status: mdm.StatusAcknowledged}, clk.Now()); err != nil {
		t.Fatal(err)
	}
	next, err := ms.Next(ctx, id, false, clk.Now())
	if err != nil || next == nil {
		t.Fatalf("missing follow-up: %v", err)
	}
	decoded, err := mdm.DecodeCommand(next.Raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok = decoded.Payload.(*commands.DeclarativeManagement)
	if !ok {
		t.Fatalf("unexpected follow-up: %T", decoded.Payload)
	}
	if !bytes.Equal(payload.Data, current) {
		t.Fatal("follow-up does not carry current token")
	}
}
