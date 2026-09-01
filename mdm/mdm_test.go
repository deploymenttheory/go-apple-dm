package mdm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
)

func TestEnrollmentResolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   mdm.Enrollment
		want mdm.EnrollmentID
		err  error
	}{
		{"device", mdm.Enrollment{UDID: "D"}, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}, nil},
		{"user", mdm.Enrollment{UDID: "D", UserID: "U"}, mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D:U", ParentID: "D"}, nil},
		{"shared ipad", mdm.Enrollment{UDID: "D", UserID: mdm.SharedIPadUserID, UserShortName: "alice"}, mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "D:alice", ParentID: "D"}, nil},
		{"shared ipad no user", mdm.Enrollment{UDID: "D", UserID: mdm.SharedIPadUserID}, mdm.EnrollmentID{}, mdm.ErrSharedIPadNoUser},
		{"user enrollment device", mdm.Enrollment{EnrollmentID: "E"}, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "E"}, nil},
		{"user enrollment user", mdm.Enrollment{EnrollmentID: "E", EnrollmentUserID: "EU"}, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentUser, ID: "E:EU", ParentID: "E"}, nil},
		{"udid wins over enrollment id", mdm.Enrollment{UDID: "D", EnrollmentID: "E"}, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}, nil},
		{"none", mdm.Enrollment{UserID: "U"}, mdm.EnrollmentID{}, mdm.ErrNoEnrollmentID},
	}
	for _, c := range cases {
		got, err := c.in.Resolve()
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		if err == nil {
			if verr := got.Validate(); verr != nil {
				t.Errorf("%s: Validate: %v", c.name, verr)
			}
			if got.String() != got.ID {
				t.Errorf("%s: String", c.name)
			}
			dev := got.Device()
			if dev.Channel.IsUser() || dev.ParentID != "" {
				t.Errorf("%s: Device() = %+v", c.name, dev)
			}
			if got.Channel.IsUser() && dev.ID != got.ParentID {
				t.Errorf("%s: Device().ID = %s, want %s", c.name, dev.ID, got.ParentID)
			}
		}
	}
	// User enrollment user maps to the user-enrollment device channel.
	ue := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentUser, ID: "E:EU", ParentID: "E"}
	if d := ue.Device(); d.Channel != mdm.ChannelUserEnrollmentDevice {
		t.Errorf("Device() of user enrollment user = %v", d.Channel)
	}
}

func TestEnrollmentIDValidate(t *testing.T) {
	t.Parallel()
	bad := []mdm.EnrollmentID{
		{},
		{Channel: mdm.ChannelDevice},
		{Channel: mdm.ChannelUser, ID: "D:U"},
		{Channel: mdm.ChannelDevice, ID: "D", ParentID: "X"},
		{Channel: mdm.Channel(99), ID: "x"},
	}
	for _, b := range bad {
		if err := b.Validate(); !errors.Is(err, mdm.ErrInvalidEnrollment) {
			t.Errorf("%+v: err = %v", b, err)
		}
	}
	for _, c := range []mdm.Channel{mdm.ChannelUnknown, mdm.ChannelDevice, mdm.ChannelUser, mdm.ChannelSharedIPadUser, mdm.ChannelUserEnrollmentDevice, mdm.ChannelUserEnrollmentUser, mdm.Channel(42)} {
		if c.String() == "" {
			t.Error("empty channel string")
		}
	}
	if mdm.ChannelUnknown.Valid() || !mdm.ChannelDevice.Valid() {
		t.Error("Valid")
	}
}

const tokenUpdate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>MessageType</key><string>TokenUpdate</string>
<key>Topic</key><string>com.apple.mgmt.External.test</string>
<key>UDID</key><string>DEVICE-1</string>
<key>PushMagic</key><string>magic</string>
<key>Token</key><data>AQID</data>
<key>UnlockToken</key><data>BAU=</data>
</dict></plist>`

const authenticate = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>MessageType</key><string>Authenticate</string>
<key>Topic</key><string>com.apple.mgmt.External.test</string>
<key>UDID</key><string>DEVICE-1</string>
<key>SerialNumber</key><string>C02XYZ</string>
<key>DeviceName</key><string>Mac</string>
<key>Model</key><string>MacBookPro18,1</string>
</dict></plist>`

func TestDecodeCheckinTypes(t *testing.T) {
	t.Parallel()
	c, err := mdm.DecodeCheckin([]byte(tokenUpdate))
	if err != nil {
		t.Fatal(err)
	}
	tu, ok := c.Message.(*checkin.TokenUpdate)
	if !ok || c.Type != "TokenUpdate" || c.ID.ID != "DEVICE-1" || c.ID.Channel != mdm.ChannelDevice {
		t.Fatalf("decoded %+v", c)
	}
	p, err := mdm.PushFromTokenUpdate(tu)
	if err != nil || p.Topic != "com.apple.mgmt.External.test" || p.Magic != "magic" || string(p.Token) != "\x01\x02\x03" {
		t.Fatalf("push = %+v, %v", p, err)
	}
	if string(tu.UnlockToken) != "\x04\x05" || !strings.Contains(string(c.Raw), "TokenUpdate") {
		t.Fatalf("unlock token or raw: %+v", tu)
	}
	a, err := mdm.DecodeCheckin([]byte(authenticate))
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := a.Message.(*checkin.Authenticate)
	if !ok || auth.SerialNumber == nil || *auth.SerialNumber != "C02XYZ" {
		t.Fatalf("authenticate %+v", a.Message)
	}
	// User channel identity resolves.
	user := strings.Replace(tokenUpdate, "<key>PushMagic</key>", "<key>UserID</key><string>U1</string><key>PushMagic</key>", 1)
	u, err := mdm.DecodeCheckin([]byte(user))
	if err != nil || u.ID.Channel != mdm.ChannelUser || u.ID.ID != "DEVICE-1:U1" || u.ID.ParentID != "DEVICE-1" {
		t.Fatalf("user channel: %+v %v", u, err)
	}
}

func TestDecodeCheckinRejects(t *testing.T) {
	t.Parallel()
	var pe *mdm.ParseError
	unknown := strings.Replace(tokenUpdate, "TokenUpdate", "Bogus", 1)
	_, err := mdm.DecodeCheckin([]byte(unknown))
	if !errors.Is(err, mdm.ErrUnknownMessageType) || !errors.As(err, &pe) || pe.Error() == "" {
		t.Fatalf("unknown type: %v", err)
	}
	noID := strings.Replace(tokenUpdate, "<key>UDID</key><string>DEVICE-1</string>", "", 1)
	if _, err := mdm.DecodeCheckin([]byte(noID)); !errors.Is(err, mdm.ErrNoEnrollmentID) {
		t.Fatalf("no id: %v", err)
	}
	if _, err := mdm.DecodeCheckin([]byte("garbage")); !errors.Is(err, plist.ErrUnknownFormat) {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := mdm.DecodeCheckin([]byte(tokenUpdate), mdm.WithLimits(plist.Decoder{MaxBytes: 10})); !errors.Is(err, plist.ErrTooLarge) {
		t.Fatalf("limits: %v", err)
	}
	if _, err := mdm.PushFromTokenUpdate(nil); err == nil {
		t.Fatal("nil TokenUpdate")
	}
	if _, err := mdm.PushFromTokenUpdate(&checkin.TokenUpdate{Topic: "t"}); !errors.Is(err, mdm.ErrInvalidEnrollment) {
		t.Fatal("incomplete TokenUpdate")
	}
}

func TestNewCommandRoundTrip(t *testing.T) {
	t.Parallel()
	msg := "locked"
	cmd, err := mdm.NewCommand(&commands.DeviceLock{Message: &msg, PIN: new("123456")})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.RequestType != "DeviceLock" || cmd.UUID == "" || !strings.Contains(string(cmd.Raw), "<key>RequestType</key><string>DeviceLock</string>") {
		t.Fatalf("command %+v\n%s", cmd, cmd.Raw)
	}
	dec, err := mdm.DecodeCommand(cmd.Raw)
	if err != nil {
		t.Fatal(err)
	}
	lock, ok := dec.Payload.(*commands.DeviceLock)
	if !ok || dec.UUID != cmd.UUID || dec.RequestType != "DeviceLock" || lock.Message == nil || *lock.Message != "locked" || *lock.PIN != "123456" {
		t.Fatalf("decoded %+v %+v", dec, dec.Payload)
	}
	fixed, err := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("FIXED-1"))
	if err != nil || fixed.UUID != "FIXED-1" {
		t.Fatalf("WithUUID: %+v %v", fixed, err)
	}
	if _, err := mdm.NewCommand(nil); !errors.Is(err, mdm.ErrInvalidCommand) {
		t.Fatal("nil payload")
	}
	// Unknown request types decode the envelope only.
	unknown := strings.Replace(string(cmd.Raw), "DeviceLock", "FutureCommand", 1)
	u, err := mdm.DecodeCommand([]byte(unknown))
	if err != nil || u.Payload != nil || u.RequestType != "FutureCommand" {
		t.Fatalf("unknown request type: %+v %v", u, err)
	}
	for _, bad := range []string{"garbage", `<plist version="1.0"><dict><key>CommandUUID</key><string>x</string></dict></plist>`} {
		if _, err := mdm.DecodeCommand([]byte(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	// Wrong payload shape for a known type fails.
	badShape := `<plist version="1.0"><dict><key>CommandUUID</key><string>x</string><key>Command</key><dict><key>RequestType</key><string>DeviceLock</string><key>Message</key><integer>1</integer></dict></dict></plist>`
	if _, err := mdm.DecodeCommand([]byte(badShape)); err == nil {
		t.Error("expected type error for bad Message type")
	}
}

const ackResponse = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CommandUUID</key><string>CMD-1</string>
<key>Status</key><string>Acknowledged</string>
<key>UDID</key><string>DEVICE-1</string>
<key>MessageResult</key><string>Success</string>
</dict></plist>`

func TestDecodeResponseTyped(t *testing.T) {
	t.Parallel()
	r, err := mdm.DecodeResponse([]byte(ackResponse), "DeviceLock")
	if err != nil {
		t.Fatal(err)
	}
	lock, ok := r.Payload.(*commands.DeviceLockResponse)
	if !ok || r.Status != mdm.StatusAcknowledged || r.CommandUUID != "CMD-1" || r.ID.ID != "DEVICE-1" || r.IsIdle() {
		t.Fatalf("response %+v", r)
	}
	if lock.MessageResult == nil || *lock.MessageResult != "Success" {
		t.Fatalf("typed payload %+v", lock)
	}
	// Unknown request type: envelope only.
	r2, err := mdm.DecodeResponse([]byte(ackResponse), "Nope")
	if err != nil || r2.Payload != nil {
		t.Fatalf("unknown request type: %+v %v", r2, err)
	}
	r3, err := mdm.DecodeResponse([]byte(ackResponse), "")
	if err != nil || r3.Payload != nil {
		t.Fatalf("empty request type: %+v %v", r3, err)
	}
	// Typed payload with a wrong shape is an error.
	bad := strings.Replace(ackResponse, "<string>Success</string>", "<integer>1</integer>", 1)
	if _, err := mdm.DecodeResponse([]byte(bad), "DeviceLock"); err == nil {
		t.Error("expected type error")
	}
}

func TestDecodeResponseIdleAndError(t *testing.T) {
	t.Parallel()
	idle := `<plist version="1.0"><dict><key>Status</key><string>Idle</string><key>UDID</key><string>D</string></dict></plist>`
	r, err := mdm.DecodeResponse([]byte(idle), "")
	if err != nil || !r.IsIdle() || r.CommandUUID != "" {
		t.Fatalf("idle: %+v %v", r, err)
	}
	errResp := `<plist version="1.0"><dict><key>Status</key><string>Error</string><key>CommandUUID</key><string>C</string><key>UDID</key><string>D</string>
<key>ErrorChain</key><array><dict><key>ErrorCode</key><integer>12021</integer><key>ErrorDomain</key><string>MCMDMErrorDomain</string><key>LocalizedDescription</key><string>bad</string></dict></array></dict></plist>`
	r, err = mdm.DecodeResponse([]byte(errResp), "DeviceLock")
	if err != nil || r.Status != mdm.StatusError || len(r.ErrorChain) != 1 || r.ErrorChain[0].ErrorCode != 12021 || r.Payload != nil {
		t.Fatalf("error: %+v %v", r, err)
	}
	notNow := strings.Replace(idle, "Idle", "NotNow", 1)
	if _, err := mdm.DecodeResponse([]byte(notNow), ""); !errors.Is(err, mdm.ErrInvalidResponse) {
		t.Fatalf("NotNow without CommandUUID should fail: %v", err)
	}
	for name, body := range map[string]string{
		"no status":      `<plist version="1.0"><dict><key>UDID</key><string>D</string></dict></plist>`,
		"unknown status": strings.Replace(idle, "Idle", "Weird", 1),
		"no identity":    `<plist version="1.0"><dict><key>Status</key><string>Idle</string></dict></plist>`,
		"garbage":        "nope",
	} {
		if _, err := mdm.DecodeResponse([]byte(body), ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	for _, s := range []mdm.Status{mdm.StatusAcknowledged, mdm.StatusError, mdm.StatusCommandFormatError, mdm.StatusIdle, mdm.StatusNotNow} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
}

func TestPushValidAndParseError(t *testing.T) {
	t.Parallel()
	if (mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}).Valid() != true || (mdm.Push{}).Valid() {
		t.Error("Push.Valid")
	}
	pe := &mdm.ParseError{Err: errors.New("x"), Content: []byte("c")}
	if pe.Error() != "mdm: parse: x" || !errors.Is(pe, pe.Err) {
		t.Error("ParseError")
	}
}

func FuzzDecodeCheckin(f *testing.F) {
	f.Add([]byte(tokenUpdate))
	f.Add([]byte(authenticate))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = mdm.DecodeCheckin(data, mdm.WithLimits(plist.Decoder{MaxBytes: 1 << 16}))
	})
}

func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte(ackResponse))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = mdm.DecodeResponse(data, "DeviceLock", mdm.WithLimits(plist.Decoder{MaxBytes: 1 << 16}))
		_, _ = mdm.DecodeCommand(data, mdm.WithLimits(plist.Decoder{MaxBytes: 1 << 16}))
	})
}
