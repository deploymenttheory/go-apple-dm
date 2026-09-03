package simulator

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sync"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
)

// Content types Apple devices send.
const (
	ContentTypeCheckin = "application/x-apple-aspen-mdm-checkin"
	ContentTypeConnect = "application/x-apple-aspen-mdm"
)

// Identity is the device's MDM identity certificate and key.
type Identity struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// HTTPError reports a non-200 response from the server.
type HTTPError struct {
	Status int
	Body   []byte
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("simulator: server returned HTTP %d: %s", e.Status, bytes.TrimSpace(e.Body))
}

// ErrTooManyCommands stops a Connect loop that never drains.
var ErrTooManyCommands = errors.New("simulator: command loop exceeded the limit")

// Reply is how the device answers one command.
type Reply struct {
	Status     mdm.Status
	ErrorChain []mdm.ErrorChainItem
	// Payload is merged into the response plist; nil sends the envelope only.
	Payload commands.Response
}

// Responder decides the reply for a command.
type Responder func(cmd *mdm.Command) Reply

// AcknowledgeAll answers every command with Acknowledged and a zero typed
// response when the schema knows the command.
func AcknowledgeAll(cmd *mdm.Command) Reply {
	r := Reply{Status: mdm.StatusAcknowledged}
	if entries := commands.ByID(cmd.RequestType); len(entries) > 0 {
		r.Payload = entries[0].NewResponse()
	}
	return r
}

// Device is a simulated enrolled device.
type Device struct {
	UDID         string
	SerialNumber string
	Model        string
	ModelName    string
	DeviceName   string
	ProductName  string
	OSVersion    string
	BuildVersion string
	Topic        string

	CheckinURL string
	ServerURL  string
	// EnrollmentID, when set, makes this a User Enrollment: check-ins carry
	// EnrollmentID (and EnrollmentUserID on the user channel) instead of
	// the UDID (decision record 0028).
	EnrollmentID string
	Client       *http.Client
	Identity     *Identity
	Responder    Responder
	// MaxCommandsPerConnect bounds one Connect loop (default 100).
	MaxCommandsPerConnect int

	PushMagic   string
	PushToken   []byte
	UnlockToken []byte

	mu       sync.Mutex
	commands []*mdm.Command
	replies  []Reply

	// Declarative management: options and the device channel's client state.
	acme        ACMEOptions
	attestation [][]byte
	attestKey   *ecdsa.PrivateKey
	ddm         ddmConfig
	ddmCh       *ddmChannel
}

// Option configures a Device.
type Option func(*Device)

// WithURLs sets the check-in and server URLs.
func WithURLs(checkin, server string) Option {
	return func(d *Device) { d.CheckinURL, d.ServerURL = checkin, server }
}

// WithIdentity sets the identity used to sign requests.
func WithIdentity(id *Identity) Option { return func(d *Device) { d.Identity = id } }

// WithClient sets the HTTP client.
func WithClient(c *http.Client) Option { return func(d *Device) { d.Client = c } }

// WithResponder sets how commands are answered.
func WithResponder(r Responder) Option { return func(d *Device) { d.Responder = r } }

// WithTopic sets the push topic (default com.apple.mgmt.External.simulator).
func WithTopic(t string) Option { return func(d *Device) { d.Topic = t } }

// New creates a device with sensible defaults for the given UDID.
func New(udid string, opts ...Option) *Device {
	d := &Device{
		UDID: udid, SerialNumber: "SIM" + udid, Model: "MacBookPro18,1", ModelName: "MacBook Pro", DeviceName: "Simulated " + udid,
		ProductName: "MacBookPro18,1", OSVersion: "26.0", BuildVersion: "26A100", Topic: "com.apple.mgmt.External.simulator",
		Client: http.DefaultClient, Responder: AcknowledgeAll, MaxCommandsPerConnect: 100,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// WithACME sets how the device answers an ACME device-attest-01 challenge
// when it applies a profile whose identity comes from ACME.
func WithACME(o ACMEOptions) Option {
	return func(d *Device) { d.acme = o }
}

// Commands returns every command received so far.
func (d *Device) Commands() []*mdm.Command {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*mdm.Command(nil), d.commands...)
}

// Replies returns every reply sent so far, aligned with Commands.
func (d *Device) Replies() []Reply {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Reply(nil), d.replies...)
}

// connectIdentity is the identity block of a Connect body.
func (d *Device) connectIdentity() map[string]any {
	if d.EnrollmentID != "" {
		return map[string]any{"EnrollmentID": d.EnrollmentID}
	}
	return map[string]any{"UDID": d.UDID}
}

func (d *Device) checkinFields(messageType string) map[string]any {
	if d.EnrollmentID != "" {
		// User Enrollment: the device identifies itself by EnrollmentID and
		// never sends its UDID.
		return map[string]any{"MessageType": messageType, "EnrollmentID": d.EnrollmentID}
	}
	return map[string]any{"MessageType": messageType, "UDID": d.UDID}
}

// Authenticate sends the Authenticate check-in message.
func (d *Device) Authenticate(ctx context.Context) error {
	f := d.checkinFields("Authenticate")
	f["Topic"] = d.Topic
	f["SerialNumber"] = d.SerialNumber
	f["Model"] = d.Model
	f["ModelName"] = d.ModelName
	f["DeviceName"] = d.DeviceName
	f["ProductName"] = d.ProductName
	f["OSVersion"] = d.OSVersion
	f["BuildVersion"] = d.BuildVersion
	_, err := d.checkin(ctx, f)
	return err
}

// TokenUpdate sends TokenUpdate, generating push magic and token on first
// use.
func (d *Device) TokenUpdate(ctx context.Context) error {
	if d.PushMagic == "" {
		d.PushMagic = "magic-" + d.UDID
	}
	if len(d.PushToken) == 0 {
		d.PushToken = make([]byte, 32)
		if _, err := rand.Read(d.PushToken); err != nil {
			return fmt.Errorf("simulator: %w", err)
		}
	}
	f := d.checkinFields("TokenUpdate")
	f["Topic"] = d.Topic
	f["PushMagic"] = d.PushMagic
	f["Token"] = d.PushToken
	f["UserLongName"] = ""
	if len(d.UnlockToken) > 0 {
		f["UnlockToken"] = d.UnlockToken
	}
	_, err := d.checkin(ctx, f)
	return err
}

// Enroll performs Authenticate followed by TokenUpdate.
func (d *Device) Enroll(ctx context.Context) error {
	if err := d.Authenticate(ctx); err != nil {
		return err
	}
	return d.TokenUpdate(ctx)
}

// Reenroll switches to a new identity and enrols again.
func (d *Device) Reenroll(ctx context.Context, id *Identity) error {
	d.Identity = id
	return d.Enroll(ctx)
}

// CheckOut sends CheckOut.
func (d *Device) CheckOut(ctx context.Context) error {
	f := d.checkinFields("CheckOut")
	f["Topic"] = d.Topic
	_, err := d.checkin(ctx, f)
	return err
}

// SetBootstrapToken escrows a bootstrap token.
func (d *Device) SetBootstrapToken(ctx context.Context, token []byte) error {
	f := d.checkinFields("SetBootstrapToken")
	f["BootstrapToken"] = token
	_, err := d.checkin(ctx, f)
	return err
}

// GetBootstrapToken retrieves the escrowed bootstrap token (nil when the
// server has none).
func (d *Device) GetBootstrapToken(ctx context.Context) ([]byte, error) {
	body, err := d.checkin(ctx, d.checkinFields("GetBootstrapToken"))
	if err != nil {
		return nil, err
	}
	var resp struct {
		BootstrapToken []byte `plist:"BootstrapToken"`
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	if err := plist.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	return resp.BootstrapToken, nil
}

// GetToken requests a token for a service type.
func (d *Device) GetToken(ctx context.Context, serviceType string) ([]byte, error) {
	f := d.checkinFields("GetToken")
	f["TokenServiceType"] = serviceType
	body, err := d.checkin(ctx, f)
	if err != nil {
		return nil, err
	}
	var resp struct {
		TokenData []byte `plist:"TokenData"`
	}
	if err := plist.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	return resp.TokenData, nil
}

// DeclarativeManagement sends a DDM check-in for an endpoint.
func (d *Device) DeclarativeManagement(ctx context.Context, endpoint string, data []byte) ([]byte, error) {
	f := d.checkinFields("DeclarativeManagement")
	f["Endpoint"] = endpoint
	if data != nil {
		f["Data"] = data
	}
	return d.checkin(ctx, f)
}

// checkin PUTs a check-in plist and returns the response body.
func (d *Device) checkin(ctx context.Context, fields map[string]any) ([]byte, error) {
	body, err := plist.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	return d.put(ctx, d.CheckinURL, ContentTypeCheckin, body)
}

// Connect polls the server URL with Idle and answers every command it is
// handed until the server returns an empty body. It returns the commands
// processed in this connection.
func (d *Device) Connect(ctx context.Context) ([]*mdm.Command, error) {
	return d.connectAs(ctx, d.connectIdentity(), d.ddmChannel())
}

// connectAs runs the command loop for one channel. A DeclarativeManagement
// command the Responder acknowledges triggers the channel's DDM client
// (sync then status report) before the reply is sent.
func (d *Device) connectAs(ctx context.Context, identity map[string]any, ddm *ddmChannel) ([]*mdm.Command, error) {
	msg := map[string]any{"Status": string(mdm.StatusIdle)}
	maps.Copy(msg, identity)
	var processed []*mdm.Command
	limit := d.MaxCommandsPerConnect
	if limit <= 0 {
		limit = 100
	}
	for range limit {
		body, err := plist.Marshal(msg)
		if err != nil {
			return processed, fmt.Errorf("simulator: %w", err)
		}
		resp, err := d.put(ctx, d.ServerURL, ContentTypeConnect, body)
		if err != nil {
			return processed, err
		}
		if len(bytes.TrimSpace(resp)) == 0 {
			return processed, nil
		}
		cmd, err := mdm.DecodeCommand(resp)
		if err != nil {
			return processed, fmt.Errorf("simulator: %w", err)
		}
		reply := d.Responder(cmd)
		if cmd.RequestType == "DeclarativeManagement" && reply.Status == mdm.StatusAcknowledged {
			reply = ddm.handleCommand(ctx, cmd, reply)
		}
		d.mu.Lock()
		d.commands = append(d.commands, cmd)
		d.replies = append(d.replies, reply)
		d.mu.Unlock()
		processed = append(processed, cmd)
		msg, err = replyFields(identity, cmd, reply)
		if err != nil {
			return processed, err
		}
	}
	return processed, ErrTooManyCommands
}

// replyFields builds the response plist dictionary for a command.
func replyFields(identity map[string]any, cmd *mdm.Command, r Reply) (map[string]any, error) {
	msg := map[string]any{"CommandUUID": cmd.UUID, "Status": string(r.Status)}
	maps.Copy(msg, identity)
	if len(r.ErrorChain) > 0 {
		msg["ErrorChain"] = r.ErrorChain
	}
	if r.Payload != nil {
		raw, err := plist.Marshal(r.Payload)
		if err != nil {
			return nil, fmt.Errorf("simulator: %w", err)
		}
		var fields map[string]any
		if err := plist.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("simulator: %w", err)
		}
		maps.Copy(msg, fields)
	}
	return msg, nil
}

// put sends a signed PUT and returns the body of a 200 response.
func (d *Device) put(ctx context.Context, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "MDM/1.0 go-apple-dm-simulator")
	if d.Identity != nil {
		sig, err := cms.Sign(body, d.Identity.Cert, d.Identity.Key)
		if err != nil {
			return nil, err
		}
		req.Header.Set(cms.HeaderName, cms.EncodeHeader(sig))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, plist.DefaultMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode, Body: data}
	}
	return data, nil
}

// User is a user channel of a device.
type User struct {
	Device    *Device
	UserID    string
	ShortName string
	LongName  string
	PushMagic string
	PushToken []byte

	ddmCh *ddmChannel
}

// User returns a user channel for the device.
func (d *Device) User(userID, shortName, longName string) *User {
	return &User{Device: d, UserID: userID, ShortName: shortName, LongName: longName}
}

// SharedIPadUser is the user channel of the person logged in to a Shared
// iPad: Apple sends the sentinel UserID and identifies the user by
// UserShortName (decision record 0029).
func (d *Device) SharedIPadUser(shortName, longName string) *User {
	return d.User(mdm.SharedIPadUserID, shortName, longName)
}

func (u *User) identity() map[string]any {
	if u.Device.EnrollmentID != "" {
		return map[string]any{"EnrollmentID": u.Device.EnrollmentID, "EnrollmentUserID": u.UserID}
	}
	f := map[string]any{"UDID": u.Device.UDID, "UserID": u.UserID}
	if u.UserID == mdm.SharedIPadUserID {
		// Apple identifies the logged-in Shared iPad user by short name.
		f["UserShortName"] = u.ShortName
	}
	return f
}

// Authenticate sends UserAuthenticate and returns the response body.
func (u *User) Authenticate(ctx context.Context, digestResponse string) ([]byte, error) {
	f := u.identity()
	f["MessageType"] = "UserAuthenticate"
	f["DigestResponse"] = digestResponse
	return u.Device.checkin(ctx, f)
}

// TokenUpdate sends the user channel TokenUpdate.
// CheckOut sends CheckOut on the user channel: the server disables this
// user only (decision record 0029).
func (u *User) CheckOut(ctx context.Context) error {
	f := u.identity()
	f["MessageType"] = "CheckOut"
	f["Topic"] = u.Device.Topic
	_, err := u.Device.checkin(ctx, f)
	return err
}

func (u *User) TokenUpdate(ctx context.Context) error {
	if u.PushMagic == "" {
		u.PushMagic = "magic-" + u.Device.UDID + "-" + u.UserID
	}
	if len(u.PushToken) == 0 {
		u.PushToken = make([]byte, 32)
		if _, err := rand.Read(u.PushToken); err != nil {
			return fmt.Errorf("simulator: %w", err)
		}
	}
	f := u.identity()
	f["MessageType"] = "TokenUpdate"
	f["Topic"] = u.Device.Topic
	f["PushMagic"] = u.PushMagic
	f["Token"] = u.PushToken
	f["UserShortName"] = u.ShortName
	f["UserLongName"] = u.LongName
	_, err := u.Device.checkin(ctx, f)
	return err
}

// Connect polls the server URL on the user channel.
func (u *User) Connect(ctx context.Context) ([]*mdm.Command, error) {
	return u.Device.connectAs(ctx, u.identity(), u.ddmChannel())
}
