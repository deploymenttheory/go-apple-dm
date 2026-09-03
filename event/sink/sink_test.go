package sink_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/event/sink"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
)

var t0 = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// sentinel values seeded into every secret-bearing field. If one reaches a
// projected record, a log line, or a webhook body, it is a leak.
const (
	secretUnlock = "SENTINEL-UNLOCK-TOKEN"
	secretPush   = "SENTINEL-PUSH-TOKEN"
	secretMagic  = "SENTINEL-PUSH-MAGIC"
	secretUser   = "SENTINEL-USER-NAME"
)

func ptr[T any](v T) *T { return &v }

// tokenUpdateWithSecrets is the payload service/checkin.go publishes verbatim
// on every TokenUpdate.
func tokenUpdateWithSecrets() *checkin.TokenUpdate {
	return &checkin.TokenUpdate{
		MessageType:           "TokenUpdate",
		Topic:                 "com.apple.mgmt.External.test",
		UDID:                  "UDID-1",
		Token:                 []byte(secretPush),
		PushMagic:             secretMagic,
		UnlockToken:           []byte(secretUnlock),
		UserLongName:          secretUser,
		UserShortName:         ptr(secretUser),
		NotOnConsole:          true,
		AwaitingConfiguration: ptr(false),
	}
}

// events covers every payload shape the module publishes, seeded with
// sentinels wherever a secret or unbounded string lives.
func events() []event.Event {
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-1"}
	return []event.Event{
		{Type: event.TokenUpdated, At: t0, Actor: "device", Enrollment: id, Data: tokenUpdateWithSecrets()},
		{Type: event.Enrolled, At: t0, Actor: "device", Enrollment: id, Data: &checkin.Authenticate{
			MessageType: "Authenticate", UDID: ptr("UDID-1"), Topic: "t",
			SerialNumber: ptr("SERIAL1"), Model: "MacBookPro18,1",
		}},
		{Type: event.CommandResult, At: t0, Actor: "device", Enrollment: id, Data: &mdm.Response{
			CommandUUID: "cmd-1", Status: "Error",
			ErrorChain: []mdm.ErrorChainItem{{
				ErrorCode: 12, ErrorDomain: "MCMDMErrorDomain",
				LocalizedDescription: secretUser, USEnglishDescription: secretUser,
			}},
			Raw: []byte(secretUnlock),
		}},
		{Type: event.CommandSent, At: t0, Actor: "server", Enrollment: id, Data: &mdm.Command{
			UUID: "cmd-1", RequestType: "DeviceInformation", Raw: []byte(secretUnlock),
		}},
		{Type: event.PushTokenInvalid, At: t0, Actor: "apns", Enrollment: id, Data: push.Result{
			Invalid: true, Status: 410, Reason: "Unregistered",
		}},
		{Type: event.DDMChanged, At: t0, Actor: "ddm", Enrollment: id, Data: []ddm.Change{
			{Seq: 1, Reason: "declaration"},
		}},
		{Type: event.CertRotated, At: t0, Actor: "device", Enrollment: id, Data: "abc123"},
		{Type: event.CertReuseDenied, At: t0, Actor: "device", Enrollment: id, Data: []mdm.EnrollmentID{{ID: "OTHER"}}},
		{Type: event.UserAuthenticated, At: t0, Actor: "device", Enrollment: id, Data: "user-1"},
		{Type: event.CheckedOut, At: t0, Actor: "device", Enrollment: id},
		{Type: event.AdminAction, At: t0, Actor: "ops", Data: map[string]any{
			"Action": "enqueue", "Method": "POST", "Path": "/x", "TokenID": "tid", "Secret": secretUnlock,
		}},
		{Type: dep.EventDeviceAdded, At: t0, Actor: "dep", Data: dep.DeviceEvent{
			Account: "acct", Device: dep.Device{SerialNumber: "SERIAL1"},
		}},
		{Type: dep.EventTokenExpiring, At: t0, Actor: "dep", Data: dep.TokenExpiringEvent{Account: "acct", Expiry: t0}},
	}
}

func sentinels() []string {
	return []string{secretUnlock, secretPush, secretMagic, secretUser}
}

// The claim this package exists to make. NanoMDM and MicroMDM both forward
// the raw check-in body, so a TokenUpdate hands the receiver UnlockToken --
// the secret that clears a device passcode -- along with the push token and
// PushMagic. Nothing seeded into a payload may survive projection.
func TestNoSecretSurvivesProjection(t *testing.T) {
	reg := sink.Default()
	for _, e := range events() {
		rec := reg.Project(e)
		encoded, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
		for _, s := range sentinels() {
			if bytes.Contains(encoded, []byte(s)) {
				t.Errorf("event %s leaked %q into its projection:\n%s", e.Type, s, encoded)
			}
		}
	}
}

// The specific field, named, so a regression says what broke.
func TestTokenUpdateProjectsOnlyOperationalFields(t *testing.T) {
	rec := sink.Default().Project(event.Event{
		Type: event.TokenUpdated, At: t0, Data: tokenUpdateWithSecrets(),
	})
	want := map[string]any{
		"topic":                  "com.apple.mgmt.External.test",
		"not_on_console":         true,
		"awaiting_configuration": false,
	}
	if len(rec.Fields) != len(want) {
		t.Fatalf("fields = %v, want exactly %v", rec.Fields, want)
	}
	for k, v := range want {
		if rec.Fields[k] != v {
			t.Errorf("field %q = %v, want %v", k, rec.Fields[k], v)
		}
	}
}

// Default-deny: the failure mode of an unregistered event is a thin record,
// never a leaked payload.
func TestUnregisteredEventProjectsMetadataOnly(t *testing.T) {
	reg := sink.NewRegistry()
	rec := reg.Project(event.Event{
		Type: event.TokenUpdated, At: t0, Actor: "device", Data: tokenUpdateWithSecrets(),
	})
	if rec.Fields != nil {
		t.Fatalf("an unregistered type published %v", rec.Fields)
	}
	if rec.Type != string(event.TokenUpdated) || rec.Actor != "device" || !rec.At.Equal(t0) {
		t.Fatalf("metadata lost: %+v", rec)
	}
	if reg.Known(event.TokenUpdated) {
		t.Fatal("an unregistered type reported itself known")
	}
}

// A projection that meets a payload it does not expect degrades to metadata
// rather than guessing.
func TestProjectionTypeMismatchIsBare(t *testing.T) {
	rec := sink.Default().Project(event.Event{
		Type: event.TokenUpdated, At: t0, Data: "not a TokenUpdate",
	})
	if rec.Fields != nil {
		t.Fatalf("a mismatched payload published %v", rec.Fields)
	}
}

// Registering nil is how a type says its metadata is the whole story. That
// has to be distinguishable from never having been considered.
func TestMetadataOnlyIsDistinctFromUnknown(t *testing.T) {
	reg := sink.NewRegistry()
	reg.Register(event.CheckedOut, nil)
	if !reg.Known(event.CheckedOut) {
		t.Fatal("a nil registration must still be known")
	}
	if got := reg.Project(event.Event{Type: event.CheckedOut}).Fields; got != nil {
		t.Fatalf("fields = %v, want none", got)
	}
	if len(reg.Types()) != 1 {
		t.Fatalf("types = %v", reg.Types())
	}
}

// Every event type the module declares must be in the table. Adding one
// without a decision about its payload should fail here, not in production.
func TestEveryEventTypeIsProjected(t *testing.T) {
	reg := sink.Default()
	declared := []event.Type{
		event.Enrolled, event.Reenrolled, event.TokenUpdated, event.CheckedOut,
		event.CertRotated, event.CommandQueued, event.CommandSent, event.CommandResult,
		event.BootstrapTokenSet, event.PushTokenInvalid, event.DDMChanged,
		event.DDMStatusReceived, event.CertReuseDenied, event.EnrollmentImported,
		event.UserAuthenticated, event.UserAuthFailed,
		event.ACMEChallengeValid, event.ACMEIssued, event.AttestationRejected,
		event.AdminAction, event.AdminDenied,
		dep.EventDeviceAdded, dep.EventDeviceModified, dep.EventDeviceDeleted,
		dep.EventDeviceAssigned, dep.EventTokenExpiring,
	}
	for _, tp := range declared {
		if !reg.Known(tp) {
			t.Errorf("event type %q has no entry in the default table", tp)
		}
	}
	// event.All is a subscription wildcard, never published, so it is the one
	// declared value the table must not carry.
	if reg.Known(event.All) {
		t.Error("the subscribe-to-everything wildcard was registered as an event")
	}
}

func TestSlogSinkWritesProjectedRecords(t *testing.T) {
	var buf bytes.Buffer
	h := sink.Slog(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	for _, e := range events() {
		if err := h(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
	}
	out := buf.String()
	for _, s := range sentinels() {
		if strings.Contains(out, s) {
			t.Errorf("the slog sink leaked %q:\n%s", s, out)
		}
	}
	for _, want := range []string{"token-updated", "not_on_console", "command-result", "MCMDMErrorDomain"} {
		if !strings.Contains(out, want) {
			t.Errorf("the slog sink dropped %q:\n%s", want, out)
		}
	}
}

// A nil logger and registry are usable defaults rather than a panic.
func TestSlogSinkDefaults(t *testing.T) {
	if err := sink.Slog(nil, nil)(context.Background(), event.Event{Type: event.CheckedOut, At: t0}); err != nil {
		t.Fatal(err)
	}
}

// Every projection must refuse a payload of the wrong type rather than guess,
// because that guard is what keeps a publisher's mistake from becoming a
// leak. Asserting it once per type also keeps a new projection from shipping
// without one.
func TestEveryProjectionRefusesAMismatchedPayload(t *testing.T) {
	reg := sink.Default()
	for _, tp := range reg.Types() {
		got := reg.Project(event.Event{Type: tp, At: t0, Data: struct{ Nope string }{secretUnlock}})
		if got.Fields != nil {
			t.Errorf("%s published %v from a payload of the wrong type", tp, got.Fields)
		}
	}
}

// The remaining payload shapes, so every projection is exercised with the
// type it expects as well as one it does not.
func TestRemainingProjections(t *testing.T) {
	reg := sink.Default()
	cases := []struct {
		name  string
		ev    event.Event
		check func(t *testing.T, f map[string]any)
	}{
		{
			name: "DEPAssignment",
			ev: event.Event{Type: dep.EventDeviceAssigned, At: t0, Data: dep.AssignmentEvent{
				Account: "acct", Assignment: dep.Assignment{SerialNumber: "SERIAL1", ProfileUUID: "puuid"},
			}},
			check: func(t *testing.T, f map[string]any) {
				if f["account"] != "acct" || f["serial_number"] != "SERIAL1" || f["profile_uuid"] != "puuid" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "ACMEIssued",
			ev: event.Event{Type: event.ACMEIssued, At: t0, Data: map[string]any{
				"serial": "01", "identifier": "id", "device": "SERIAL1", "private": secretUnlock,
			}},
			check: func(t *testing.T, f map[string]any) {
				if _, ok := f["private"]; ok {
					t.Fatalf("passthrough forwarded an unlisted key: %v", f)
				}
				if f["serial"] != "01" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "CommandQueued",
			ev:   event.Event{Type: event.CommandQueued, At: t0, Data: &mdm.Command{UUID: "u", RequestType: "DeviceLock"}},
			check: func(t *testing.T, f map[string]any) {
				if f["command_uuid"] != "u" || f["request_type"] != "DeviceLock" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
		{
			name: "UserAuthFailed",
			ev:   event.Event{Type: event.UserAuthFailed, At: t0, Data: "user-9"},
			check: func(t *testing.T, f map[string]any) {
				if f["user_id"] != "user-9" {
					t.Fatalf("fields = %v", f)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, reg.Project(tc.ev).Fields)
		})
	}
}

// An empty string payload projects nothing rather than an empty key.
func TestEmptyStringPayloadIsBare(t *testing.T) {
	if f := sink.Default().Project(event.Event{Type: event.CertRotated, At: t0, Data: ""}).Fields; f != nil {
		t.Fatalf("fields = %v, want none", f)
	}
}
