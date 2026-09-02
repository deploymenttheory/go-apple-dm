package simulator

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddmproto"
)

// Declarative management client (decision record 0024). The simulator runs
// Apple's synchronization loop against the check-in endpoints ("tokens",
// "declaration-items", "declaration/<kind>/<identifier>", "status"), keeps
// per-channel state, grades every declaration with Apple's reason codes, and
// posts full or incremental status reports.
//
// Apple documentation:
//   - https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - https://developer.apple.com/documentation/devicemanagement/status-items

// Errors from the DDM client.
var (
	// ErrDDMNotSettled reports that the declarations token kept changing for
	// MaxRounds rounds, so the sync never converged.
	ErrDDMNotSettled = errors.New("simulator: declarations token did not settle")
	// ErrDDMBadResponse reports a DDM response that does not decode, is
	// missing a required key, or carries a body where none is expected.
	ErrDDMBadResponse = errors.New("simulator: malformed declarative management response")
	// ErrDDMFault is returned when an injected DDMFaults fault fires.
	ErrDDMFault = errors.New("simulator: injected declarative management fault")
)

// ddmErrorDomain is the ErrorChain domain the simulator uses when a
// DeclarativeManagement command fails.
const ddmErrorDomain = "GoAppleMDMSimulatorDDMErrorDomain"

// defaultDDMMaxRounds bounds a SyncDDM loop when WithDDMMaxRounds is unset.
const defaultDDMMaxRounds = 5

// DDMFaults injects client misbehaviour for server tests.
type DDMFaults struct {
	// DropStatus makes PostDDMStatus build the report but never send it.
	DropStatus bool
	// StaleToken makes SyncDDM forget the stored token and every
	// ServerToken, so the manifest and all declarations are fetched again.
	StaleToken bool
	// FailFetch makes the next declaration fetch fail with ErrDDMFault once.
	FailFetch bool
}

// DDMReason is one entry in a declaration's status reasons.
type DDMReason struct {
	Code        string         `json:"code"`
	Description *string        `json:"description,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// DDMDeclaration is a declaration the client holds, with its graded state.
type DDMDeclaration struct {
	Kind        schemaddm.Kind
	Type        string
	Identifier  string
	ServerToken string
	Payload     map[string]any
	// Graded state from the last DDMStatusReport.
	Active  bool
	Valid   string
	Reasons []DDMReason
}

// DDMState is one channel's declarative management state.
type DDMState struct {
	// DeclarationsToken is the token of the manifest last synchronized.
	DeclarationsToken string
	// TokenHint is the token carried by the last DeclarativeManagement
	// command's Data.
	TokenHint string
	// Items is the manifest last fetched; nil before the first sync.
	Items *ddmproto.DeclarationItemsResponse
	// Declarations is keyed by "<kind>/<identifier>".
	Declarations map[string]*DDMDeclaration
	// Properties are the @property values in effect: WithDDM properties with
	// every management.properties declaration merged on top.
	Properties map[string]any
	LastSync   time.Time
	LastReport time.Time
}

// DDMSyncResult describes one SyncDDM call.
type DDMSyncResult struct {
	// Rounds is how many tokens fetches it took to settle.
	Rounds int
	// Fetched and Removed list "<kind>/<identifier>" keys.
	Fetched []string
	Removed []string
	// Token is the declarations token in effect after the sync.
	Token string
	// Changed reports whether any declaration or the token changed.
	Changed bool
}

// ddmConfig holds the device-wide DDM options.
type ddmConfig struct {
	props     map[string]any
	maxRounds int
	faults    DDMFaults
	testItems bool
}

// WithDDM sets the activation properties the client evaluates @property
// references against. Properties from management.properties declarations
// the server sends are merged on top.
func WithDDM(props map[string]any) Option {
	return func(d *Device) { d.ddm.props = maps.Clone(props) }
}

// WithDDMMaxRounds bounds the SyncDDM convergence loop (default 5).
func WithDDMMaxRounds(n int) Option { return func(d *Device) { d.ddm.maxRounds = n } }

// WithDDMFaults injects client faults.
func WithDDMFaults(f DDMFaults) Option { return func(d *Device) { d.ddm.faults = f } }

// WithDDMTestStatusItems includes Apple's "test.*" status items in the
// reported client capabilities.
func WithDDMTestStatusItems(on bool) Option { return func(d *Device) { d.ddm.testItems = on } }

// ddmChannel is the DDM client for one channel (device or user).
type ddmChannel struct {
	dev      *Device
	identity func() map[string]any

	mu       sync.Mutex
	state    DDMState
	baseline map[string]string // graded row per key at the last posted report
}

func newDDMChannel(d *Device, identity func() map[string]any) *ddmChannel {
	return &ddmChannel{dev: d, identity: identity, state: DDMState{Declarations: map[string]*DDMDeclaration{}}}
}

// ddmChannel returns the device channel's client, creating it on first use.
func (d *Device) ddmChannel() *ddmChannel {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ddmCh == nil {
		d.ddmCh = newDDMChannel(d, func() map[string]any { return map[string]any{"UDID": d.UDID} })
	}
	return d.ddmCh
}

// ddmChannel returns the user channel's client, creating it on first use.
func (u *User) ddmChannel() *ddmChannel {
	u.Device.mu.Lock()
	defer u.Device.mu.Unlock()
	if u.ddmCh == nil {
		u.ddmCh = newDDMChannel(u.Device, u.identity)
	}
	return u.ddmCh
}

// ddmConfig snapshots the options under the device lock.
func (d *Device) ddmConfig() ddmConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ddm
}

// takeFailFetch consumes the one-shot FailFetch fault.
func (d *Device) takeFailFetch() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.ddm.faults.FailFetch {
		return false
	}
	d.ddm.faults.FailFetch = false
	return true
}

// DDM returns a snapshot of the device channel's DDM state.
func (d *Device) DDM() *DDMState { return d.ddmChannel().snapshot() }

// SyncDDM runs the synchronization loop on the device channel.
func (d *Device) SyncDDM(ctx context.Context) (DDMSyncResult, error) { return d.ddmChannel().sync(ctx) }

// DDMStatusReport builds the device channel's status report from state. A
// full report carries every declaration, the device items, and the client
// capabilities; an incremental one carries only declarations whose graded
// state changed since the last posted report (plus the device items and
// capabilities before any report has been posted).
func (d *Device) DDMStatusReport(full bool) []byte { return d.ddmChannel().report(full) }

// PostDDMStatus builds and posts a status report on the device channel.
func (d *Device) PostDDMStatus(ctx context.Context, full bool) error {
	return d.ddmChannel().post(ctx, full)
}

// DDM returns a snapshot of the user channel's DDM state.
func (u *User) DDM() *DDMState { return u.ddmChannel().snapshot() }

// SyncDDM runs the synchronization loop on the user channel.
func (u *User) SyncDDM(ctx context.Context) (DDMSyncResult, error) { return u.ddmChannel().sync(ctx) }

// DDMStatusReport builds the user channel's status report; see
// Device.DDMStatusReport.
func (u *User) DDMStatusReport(full bool) []byte { return u.ddmChannel().report(full) }

// PostDDMStatus builds and posts a status report on the user channel.
func (u *User) PostDDMStatus(ctx context.Context, full bool) error {
	return u.ddmChannel().post(ctx, full)
}

// snapshot copies the state so callers can inspect it without racing the
// client.
func (c *ddmChannel) snapshot() *DDMState {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	if c.state.Items != nil {
		items := *c.state.Items
		s.Items = &items
	}
	s.Declarations = make(map[string]*DDMDeclaration, len(c.state.Declarations))
	for k, d := range c.state.Declarations {
		cp := *d
		cp.Payload = maps.Clone(d.Payload)
		cp.Reasons = slices.Clone(d.Reasons)
		s.Declarations[k] = &cp
	}
	s.Properties = maps.Clone(c.state.Properties)
	return &s
}

// call sends one DeclarativeManagement check-in on this channel.
func (c *ddmChannel) call(ctx context.Context, endpoint string, data []byte) ([]byte, error) {
	f := c.identity()
	f["MessageType"] = "DeclarativeManagement"
	f["Endpoint"] = endpoint
	if data != nil {
		f["Data"] = data
	}
	return c.dev.checkin(ctx, f)
}

func ddmKey(kind schemaddm.Kind, identifier string) string { return string(kind) + "/" + identifier }

// sync is the bounded convergence loop: tokens; stop when the token is
// unchanged and a manifest is held; otherwise declaration-items, fetch every
// declaration whose ServerToken differs (a 404 removes it), drop identifiers
// absent from the manifest, and repeat while the token moved during the
// round. Fleet issue 43050 is the failure mode MaxRounds guards against.
func (c *ddmChannel) sync(ctx context.Context) (DDMSyncResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := c.dev.ddmConfig()
	maxRounds := cfg.maxRounds
	if maxRounds <= 0 {
		maxRounds = defaultDDMMaxRounds
	}
	stale := cfg.faults.StaleToken
	start := c.state.DeclarationsToken
	var res DDMSyncResult
	for res.Rounds < maxRounds {
		res.Rounds++
		tok, err := c.fetchTokens(ctx)
		if err != nil {
			return res, err
		}
		res.Token = tok
		if tok == c.state.DeclarationsToken && c.state.Items != nil && !stale {
			c.state.LastSync = time.Now()
			return res, nil
		}
		items, err := c.fetchItems(ctx)
		if err != nil {
			return res, err
		}
		if err := c.reconcile(ctx, items, stale, &res); err != nil {
			return res, err
		}
		c.state.Items = items
		c.state.DeclarationsToken = items.DeclarationsToken
		res.Token = items.DeclarationsToken
		res.Changed = len(res.Fetched) > 0 || len(res.Removed) > 0 || res.Token != start
		if items.DeclarationsToken == tok {
			c.state.LastSync = time.Now()
			return res, nil
		}
	}
	return res, fmt.Errorf("%w after %d rounds", ErrDDMNotSettled, res.Rounds)
}

// fetchTokens returns the server's DeclarationsToken.
func (c *ddmChannel) fetchTokens(ctx context.Context) (string, error) {
	body, err := c.call(ctx, "tokens", nil)
	if err != nil {
		return "", err
	}
	tok, err := declarationsToken(body)
	if err != nil {
		return "", fmt.Errorf("%w: tokens: %w", ErrDDMBadResponse, err)
	}
	return tok, nil
}

// errNoToken reports a TokensResponse without a DeclarationsToken.
var errNoToken = errors.New("SyncTokens.DeclarationsToken missing")

// declarationsToken decodes a TokensResponse strictly and extracts the
// DeclarationsToken.
func declarationsToken(body []byte) (string, error) {
	var tr ddmproto.TokensResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	tok, _ := tr.SyncTokens["DeclarationsToken"].(string)
	if tok == "" {
		return "", errNoToken
	}
	return tok, nil
}

// fetchItems returns the declaration manifest.
func (c *ddmChannel) fetchItems(ctx context.Context) (*ddmproto.DeclarationItemsResponse, error) {
	body, err := c.call(ctx, "declaration-items", nil)
	if err != nil {
		return nil, err
	}
	var items ddmproto.DeclarationItemsResponse
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("%w: declaration-items: %w", ErrDDMBadResponse, err)
	}
	if items.DeclarationsToken == "" {
		return nil, fmt.Errorf("%w: declaration-items: DeclarationsToken missing", ErrDDMBadResponse)
	}
	return &items, nil
}

// ddmWire is the top level of a fetched declaration.
type ddmWire struct {
	Type        string         `json:"Type"`
	Identifier  string         `json:"Identifier"`
	ServerToken string         `json:"ServerToken"`
	Payload     map[string]any `json:"Payload"`
}

// fetchDeclaration fetches one declaration; a 404 is returned as *HTTPError.
func (c *ddmChannel) fetchDeclaration(ctx context.Context, kind schemaddm.Kind, identifier string) (*DDMDeclaration, error) {
	endpoint := "declaration/" + ddmKey(kind, identifier)
	if c.dev.takeFailFetch() {
		return nil, fmt.Errorf("%w: %s", ErrDDMFault, endpoint)
	}
	body, err := c.call(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var w ddmWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDDMBadResponse, endpoint, err)
	}
	if w.Type == "" || w.Identifier != identifier {
		return nil, fmt.Errorf("%w: %s: Type or Identifier missing or mismatched", ErrDDMBadResponse, endpoint)
	}
	if w.Payload == nil {
		w.Payload = map[string]any{}
	}
	return &DDMDeclaration{Kind: kind, Type: w.Type, Identifier: w.Identifier, ServerToken: w.ServerToken, Payload: w.Payload}, nil
}

// reconcile brings the stored declarations in line with a manifest.
func (c *ddmChannel) reconcile(ctx context.Context, items *ddmproto.DeclarationItemsResponse, stale bool, res *DDMSyncResult) error {
	groups := []struct {
		kind schemaddm.Kind
		refs []ddmproto.DeclarationItemsResponseDeclarationItem
	}{
		{schemaddm.KindActivation, items.Declarations.Activations},
		{schemaddm.KindConfiguration, items.Declarations.Configurations},
		{schemaddm.KindAsset, items.Declarations.Assets},
		{schemaddm.KindManagement, items.Declarations.Management},
	}
	want := map[string]bool{}
	for _, g := range groups {
		for _, ref := range g.refs {
			key := ddmKey(g.kind, ref.Identifier)
			want[key] = true
			if have, ok := c.state.Declarations[key]; ok && !stale && have.ServerToken == ref.ServerToken {
				continue
			}
			decl, err := c.fetchDeclaration(ctx, g.kind, ref.Identifier)
			if err != nil {
				var he *HTTPError
				if errors.As(err, &he) && he.Status == http.StatusNotFound {
					want[key] = false
					continue
				}
				return err
			}
			c.state.Declarations[key] = decl
			res.Fetched = append(res.Fetched, key)
		}
	}
	for key := range c.state.Declarations {
		if !want[key] {
			delete(c.state.Declarations, key)
			res.Removed = append(res.Removed, key)
		}
	}
	slices.Sort(res.Removed)
	return nil
}

// post builds the report and sends it to the "status" endpoint, which must
// answer 200 with an empty body. DropStatus builds but does not send.
func (c *ddmChannel) post(ctx context.Context, full bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := c.dev.ddmConfig()
	body, rows := c.buildReport(cfg, full)
	if cfg.faults.DropStatus {
		return nil
	}
	resp, err := c.call(ctx, "status", body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(resp)) != 0 {
		return fmt.Errorf("%w: status: unexpected body %q", ErrDDMBadResponse, bytes.TrimSpace(resp))
	}
	c.baseline = rows
	c.state.LastReport = time.Now()
	return nil
}

// report builds the status report without posting it.
func (c *ddmChannel) report(full bool) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, _ := c.buildReport(c.dev.ddmConfig(), full)
	return body
}

// handleCommand runs the sync and the status report for an acknowledged
// DeclarativeManagement command; any failure turns the reply into an Error
// with a one-item error chain.
func (c *ddmChannel) handleCommand(ctx context.Context, cmd *mdm.Command, reply Reply) Reply {
	if dm, ok := cmd.Payload.(*commands.DeclarativeManagement); ok && len(dm.Data) > 0 {
		tok, err := declarationsToken(dm.Data)
		if err != nil {
			return ddmErrorReply(fmt.Errorf("%w: command Data: %w", ErrDDMBadResponse, err))
		}
		c.mu.Lock()
		c.state.TokenHint = tok
		c.mu.Unlock()
	}
	if _, err := c.sync(ctx); err != nil {
		return ddmErrorReply(err)
	}
	c.mu.Lock()
	full := c.state.LastReport.IsZero()
	c.mu.Unlock()
	if err := c.post(ctx, full); err != nil {
		return ddmErrorReply(err)
	}
	return reply
}

func ddmErrorReply(err error) Reply {
	return Reply{Status: mdm.StatusError, ErrorChain: []mdm.ErrorChainItem{{
		ErrorCode: 1, ErrorDomain: ddmErrorDomain, LocalizedDescription: err.Error(), USEnglishDescription: err.Error(),
	}}}
}
