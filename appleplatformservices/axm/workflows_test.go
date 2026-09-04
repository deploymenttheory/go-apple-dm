package axm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestActivities(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	t.Run("AssignBody", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.AddOrgDevice("S2", nil)
		c := f.client(t, nil)
		act, err := c.AssignDevices(context.Background(), id, []string{"S1", "S2"})
		if err != nil || act.Attributes.ActivityType != ActivityAssignDevices || act.ID == "" {
			t.Fatalf("%+v %v", act, err)
		}
		body := decodeBody(t, f.srv.LastRequest())
		if dig(t, body, "data", "type") != "orgDeviceActivities" || dig(t, body, "data", "attributes", "activityType") != "ASSIGN_DEVICES" {
			t.Fatalf("%v", body)
		}
		if dig(t, body, "data", "relationships", "mdmServer", "data", "type") != "mdmServers" || dig(t, body, "data", "relationships", "mdmServer", "data", "id") != id {
			t.Fatalf("%v", body)
		}
		devices := dig(t, body, "data", "relationships", "devices", "data").([]any)
		if len(devices) != 2 || dig(t, devices[0], "type") != "orgDevices" || dig(t, devices[1], "id") != "S2" {
			t.Fatalf("%v", devices)
		}
		if _, has := dig(t, body, "data", "attributes").(map[string]any)["activityTypeMetadata"]; has {
			t.Fatal("metadata must be absent")
		}
	})
	t.Run("UnassignBody", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		if _, err := c.UnassignDevices(context.Background(), []string{"S1"}); err != nil {
			t.Fatal(err)
		}
		body := decodeBody(t, f.srv.LastRequest())
		if dig(t, body, "data", "attributes", "activityType") != "UNASSIGN_DEVICES" {
			t.Fatalf("%v", body)
		}
		if _, has := dig(t, body, "data", "relationships").(map[string]any)["mdmServer"]; has {
			t.Fatal("mdmServer must be absent")
		}
	})
	t.Run("ReleaseBody", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		act, err := c.ReleaseDevices(context.Background(), []string{"S1"})
		if err != nil {
			t.Fatal(err)
		}
		body := decodeBody(t, f.srv.LastRequest())
		if dig(t, body, "data", "attributes", "activityType") != "RELEASE_DEVICES" {
			t.Fatalf("%v", body)
		}
		if _, has := dig(t, body, "data", "relationships").(map[string]any)["mdmServer"]; has {
			t.Fatal("mdmServer must be absent")
		}
		f.srv.Complete()
		if f.srv.HasOrgDevice("S1") {
			t.Fatal("released device still in the organization")
		}
		got, err := c.GetOrgDeviceActivity(context.Background(), act.ID, GetOptions{})
		if err != nil || !got.Succeeded() {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("MigrationDeadlineRules", func(t *testing.T) {
		t.Parallel()
		ok := now.Add(30 * 24 * time.Hour)
		late := now.Add(91 * 24 * time.Hour)
		if _, err := NewActivityRequest(ActivityAssignDevicesWithMigrationDeadline, "srv", []string{"S"}, late, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("late: %v", err)
		}
		if _, err := NewActivityRequest(ActivityAssignDevicesWithMigrationDeadline, "srv", []string{"S"}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("missing: %v", err)
		}
		if _, err := NewActivityRequest(ActivityUpdateMigrationDeadline, "", []string{"S"}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("update missing: %v", err)
		}
		if _, err := NewActivityRequest(ActivityUpdateMigrationDeadline, "", []string{"S"}, late, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("update late: %v", err)
		}
		if _, err := NewActivityRequest(ActivityAssignDevices, "srv", []string{"S"}, ok, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("deadline on plain assign: %v", err)
		}
		req, err := NewActivityRequest(ActivityAssignDevicesWithMigrationDeadline, "srv", []string{"S"}, ok.In(time.FixedZone("x", 3600)), now)
		if err != nil || req.Data.Attributes.ActivityTypeMetadata == nil || !req.Data.Attributes.ActivityTypeMetadata.MDMMigrationDeadlineDateTime.Equal(ok) || req.Data.Attributes.ActivityTypeMetadata.MDMMigrationDeadlineDateTime.Location() != time.UTC {
			t.Fatalf("%+v %v", req, err)
		}
		req, err = NewActivityRequest(ActivityUpdateMigrationDeadline, "", []string{"S"}, now.Add(90*24*time.Hour), now)
		if err != nil || req.Data.Relationships.MDMServer != nil {
			t.Fatalf("exactly 90 days: %+v %v", req, err)
		}
		req, err = NewActivityRequest(ActivityCancelMigration, "", []string{"S"}, time.Time{}, now)
		if err != nil || req.Data.Attributes.ActivityTypeMetadata != nil {
			t.Fatalf("cancel: %+v %v", req, err)
		}
		if _, err := NewActivityRequest(ActivityCancelMigration, "", []string{"S"}, ok, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("cancel with deadline: %v", err)
		}
		// The workflow methods apply the rules and the fake enforces them
		// too (409).
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		if _, err := c.AssignWithMigrationDeadline(context.Background(), id, []string{"S1"}, time.Now().Add(100*24*time.Hour)); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("client rule: %v", err)
		}
		req, _ = NewActivityRequest(ActivityAssignDevicesWithMigrationDeadline, id, []string{"S1"}, time.Now().Add(24*time.Hour), time.Now())
		req.Data.Attributes.ActivityTypeMetadata.MDMMigrationDeadlineDateTime = time.Now().Add(100 * 24 * time.Hour)
		if _, err := c.CreateOrgDeviceActivity(context.Background(), req); !IsConflict(err) {
			t.Fatalf("server rule: %v", err)
		}
		act, err := c.AssignWithMigrationDeadline(context.Background(), id, []string{"S1"}, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		f.srv.Complete()
		if got, _, _ := f.srv.Activity(act.ID); got != "COMPLETED" {
			t.Fatal(got)
		}
		if f.srv.DeviceAttribute("S1", "mdmMigrationStatus") != "REQUESTED" {
			t.Fatal("migration not recorded")
		}
		if _, err := c.UpdateMigrationDeadline(context.Background(), []string{"S1"}, time.Now().Add(48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.CancelMigration(context.Background(), []string{"S1"}); err != nil {
			t.Fatal(err)
		}
		f.srv.Complete()
		if f.srv.DeviceAttribute("S1", "mdmMigrationStatus") != nil {
			t.Fatal("migration not cancelled")
		}
		// Cancelling again fails per serial but the activity completes.
		act, err = c.CancelMigration(context.Background(), []string{"S1"})
		if err != nil {
			t.Fatal(err)
		}
		f.srv.Complete()
		got, err := c.GetOrgDeviceActivity(context.Background(), act.ID, GetOptions{})
		if err != nil || got.Attributes.SubStatus != ActivityCompletedWithError {
			t.Fatalf("%+v %v", got.Attributes, err)
		}
	})
	t.Run("ServerRequired", func(t *testing.T) {
		t.Parallel()
		if _, err := NewActivityRequest(ActivityAssignDevices, "", []string{"S"}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("assign without server: %v", err)
		}
		if _, err := NewActivityRequest(ActivityAssignDevicesWithMigrationDeadline, "", []string{"S"}, now.Add(time.Hour), now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("assign with deadline without server: %v", err)
		}
		for _, typ := range []OrgDeviceActivityType{ActivityUnassignDevices, ActivityReleaseDevices, ActivityCancelMigration} {
			if _, err := NewActivityRequest(typ, "srv", []string{"S"}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
				t.Errorf("%s with server: %v", typ, err)
			}
		}
		if _, err := NewActivityRequest(ActivityAssignDevices, "srv", nil, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("no serials: %v", err)
		}
		if _, err := NewActivityRequest(ActivityAssignDevices, "srv", []string{""}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("empty serial: %v", err)
		}
		if _, err := NewActivityRequest("BOGUS", "", []string{"S"}, time.Time{}, now); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("unknown type: %v", err)
		}
		f := newFixture(t)
		c := f.client(t, nil)
		if _, err := c.AssignDevices(context.Background(), "", []string{"S"}); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("AssignDevices: %v", err)
		}
		if _, err := c.UnassignDevices(context.Background(), nil); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("UnassignDevices: %v", err)
		}
		if _, err := c.ReleaseDevices(context.Background(), nil); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("ReleaseDevices: %v", err)
		}
		if _, err := c.UpdateMigrationDeadline(context.Background(), []string{"S"}, time.Time{}); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("UpdateMigrationDeadline: %v", err)
		}
		if _, err := c.CancelMigration(context.Background(), nil); !errors.Is(err, ErrActivityRule) {
			t.Fatalf("CancelMigration: %v", err)
		}
		// Unknown server and serial are 409 from the API.
		f.srv.AddOrgDevice("S1", nil)
		if _, err := c.AssignDevices(context.Background(), "0000", []string{"S1"}); !IsConflict(err) {
			t.Fatalf("unknown server: %v", err)
		}
		id := f.srv.AddMDMServer("m", nil)
		if _, err := c.AssignDevices(context.Background(), id, []string{"NOPE"}); !IsConflict(err) {
			t.Fatalf("unknown serial: %v", err)
		}
	})
	t.Run("WaitForActivityTerminal", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.AddOrgDevice("S2", nil)
		f.srv.SetOutcome("S2", "device is locked")
		c := f.client(t, nil)
		act, err := c.AssignDevices(context.Background(), id, []string{"S1", "S2"})
		if err != nil {
			t.Fatal(err)
		}
		f.srv.AutoAdvance(2 * time.Millisecond)
		done, err := c.WaitForActivity(context.Background(), act.ID, WaitOptions{Interval: time.Millisecond, Timeout: 5 * time.Second, Backoff: 2, MaxInterval: 4 * time.Millisecond})
		if err != nil || !done.Terminal() || done.Attributes.Status != ActivityCompleted || done.Attributes.SubStatus != ActivityCompletedWithError {
			t.Fatalf("%+v %v", done, err)
		}
		if done.Attributes.DownloadURL == "" || done.Attributes.CompletedDateTime.IsZero() {
			t.Fatalf("%+v", done.Attributes)
		}
		delays := f.clock.recorded()
		if len(delays) == 0 || delays[0] != time.Millisecond {
			t.Fatalf("delays %v", delays)
		}
		for _, d := range delays {
			if d > 4*time.Millisecond {
				t.Fatalf("interval above MaxInterval: %v", delays)
			}
		}
		if f.srv.AssignedServer("S1") != id || f.srv.AssignedServer("S2") != "" {
			t.Fatal("per-serial outcome not applied")
		}
		// Already terminal returns at once.
		f.clock.delays = nil
		if _, err := c.WaitForActivity(context.Background(), act.ID, WaitOptions{}); err != nil || len(f.clock.recorded()) != 0 {
			t.Fatalf("%v %v", err, f.clock.recorded())
		}
		if _, err := c.WaitForActivity(context.Background(), "", WaitOptions{}); !errors.Is(err, ErrArgument) {
			t.Fatalf("empty id: %v", err)
		}
		if _, err := c.WaitForActivity(context.Background(), "missing", WaitOptions{}); !IsNotFound(err) {
			t.Fatalf("missing: %v", err)
		}
	})
	t.Run("WaitTimeout", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		act, err := c.AssignDevices(context.Background(), id, []string{"S1"})
		if err != nil {
			t.Fatal(err)
		}
		last, err := c.WaitForActivity(context.Background(), act.ID, WaitOptions{Interval: time.Millisecond, Timeout: 20 * time.Millisecond})
		if !errors.Is(err, ErrWaitTimeout) || last == nil || last.Attributes.Status != ActivityInProgress {
			t.Fatalf("%+v %v", last, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.WaitForActivity(ctx, act.ID, WaitOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
		o := WaitOptions{Backoff: 0.5, MaxInterval: time.Millisecond, Interval: time.Second}.defaults()
		if o.Backoff != 1 || o.MaxInterval != time.Second || o.Timeout != DefaultWaitTimeout {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("FetchActivityLog", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.AddOrgDevice("S2", nil)
		f.srv.SetOutcome("S2", "device is locked")
		c := f.client(t, nil)
		act, err := c.AssignDevices(context.Background(), id, []string{"S1", "S2"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.FetchActivityLog(context.Background(), f.srv.URL+"/v1/orgDeviceActivities/"+act.ID+"/download"); !IsNotFound(err) {
			t.Fatalf("log before completion: %v", err)
		}
		f.srv.Complete()
		done, err := c.WaitForActivity(context.Background(), act.ID, WaitOptions{})
		if err != nil {
			t.Fatal(err)
		}
		body, err := c.FetchActivityLog(context.Background(), done.Attributes.DownloadURL)
		if err != nil {
			t.Fatal(err)
		}
		csv := readAll(t, body)
		if !strings.HasPrefix(csv, "serialNumber,activityType,status,reason\n") || !strings.Contains(csv, "S1,ASSIGN_DEVICES,SUCCESS,") || !strings.Contains(csv, "S2,ASSIGN_DEVICES,FAILED,device is locked") {
			t.Fatalf("csv %q", csv)
		}
		if got := f.srv.LastRequest().Header.Get("Accept"); !strings.Contains(got, "text/csv") {
			t.Fatalf("Accept %q", got)
		}
		if _, err := c.FetchActivityLog(context.Background(), "https://cdn.example.com/log.csv"); !errors.Is(err, ErrForeignHost) {
			t.Fatalf("foreign host: %v", err)
		}
		if _, err := c.FetchActivityLog(context.Background(), "http://[::1]:x/"); !errors.Is(err, ErrArgument) {
			t.Fatalf("unparsable: %v", err)
		}
	})
}

func TestAssignment(t *testing.T) {
	t.Parallel()
	t.Run("ConvergenceToleratesEmptyAnd404", func(t *testing.T) {
		t.Parallel()
		for _, mode := range []string{"empty", "404"} {
			f := newFixture(t)
			f.srv.UnassignedLinkage404(mode == "404")
			id := f.srv.AddMDMServer("m", nil)
			f.srv.AddOrgDevice("S1", nil)
			// A read count rather than a wall-clock lag, so the client is
			// observed to poll however fast or slow the machine is.
			f.srv.SetConsistencyReads(2)
			c := f.client(t, nil)
			act, err := c.AssignDevices(context.Background(), id, []string{"S1"})
			if err != nil {
				t.Fatal(err)
			}
			f.srv.Complete()
			if got, _, _ := f.srv.Activity(act.ID); got != "COMPLETED" {
				t.Fatalf("%s: activity %s", mode, got)
			}
			link, err := c.GetAssignedServerLinkage(context.Background(), "S1")
			switch mode {
			case "empty":
				if err != nil || link.ID != "" {
					t.Fatalf("empty mode before convergence: %+v %v", link, err)
				}
			default:
				if !IsNotFound(err) {
					t.Fatalf("404 mode before convergence: %v", err)
				}
			}
			if err := c.WaitForAssignedServer(context.Background(), "S1", id, 5*time.Second); err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			if n := len(f.clock.recorded()); n == 0 {
				t.Fatalf("%s: expected polling delays", mode)
			}
			for _, d := range f.clock.recorded() {
				if d > AssignmentPollInterval {
					t.Fatalf("%s: delay %v", mode, d)
				}
			}
			// Unassignment converges to empty or 404.
			if _, err := c.UnassignDevices(context.Background(), []string{"S1"}); err != nil {
				t.Fatal(err)
			}
			f.srv.Complete()
			if err := c.WaitForAssignedServer(context.Background(), "S1", "", 5*time.Second); err != nil {
				t.Fatalf("%s unassign: %v", mode, err)
			}
			// Timeout while nothing changes.
			if err := c.WaitForAssignedServer(context.Background(), "S1", id, 10*time.Millisecond); !errors.Is(err, ErrWaitTimeout) {
				t.Fatalf("%s timeout: %v", mode, err)
			}
			f.srv.Close()
		}
	})
	t.Run("Failing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		if err := c.WaitForAssignedServer(context.Background(), "", "x", time.Second); !errors.Is(err, ErrArgument) {
			t.Fatalf("empty serial: %v", err)
		}
		f.srv.ServerError(10)
		c = f.client(t, func(cfg *Config) { cfg.Retry.Max = 0 })
		if err := c.WaitForAssignedServer(context.Background(), "S1", "x", time.Second); !hasStatus(err, http.StatusServiceUnavailable) {
			t.Fatalf("API error must surface: %v", err)
		}
		f.srv.ServerError(0)
		f.srv.AddOrgDevice("S1", nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.WaitForAssignedServer(ctx, "S1", "x", 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
	})
}
