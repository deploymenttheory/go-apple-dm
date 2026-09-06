package dep

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Service paths, from the endpoint pages under Device assignment.
const (
	PathSession       = "/session"
	PathAccount       = "/account"
	PathFetchDevices  = "/server/devices"
	PathSyncDevices   = "/devices/sync"
	PathDeviceDetails = "/devices"
	PathDisown        = "/devices/disown"
	PathActivationLck = "/device/activationlock"
	PathProfile       = "/profile"
	PathProfileDevs   = "/profile/devices"
	PathDiscovery     = "/account-driven-enrollment/profile"
	PathBetaTokens    = "/os-beta-enrollment/tokens" // #nosec G101 -- an API path, not a credential
)

// FallbackLimit is the page limit used when the account detail carries
// none for an endpoint; Apple documents 1000 as the maximum.
const FallbackLimit = 1000

// call builds and sends one JSON request.
func (c *Client) call(ctx context.Context, account, method, path string, query url.Values, body, out any) error {
	req, err := c.NewRequest(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	return c.Do(ctx, account, req, out)
}

// Account fetches the account detail (GET /account).
func (c *Client) Account(ctx context.Context, account string) (*AccountDetail, error) {
	var out AccountDetail
	if err := c.call(ctx, account, http.MethodGet, PathAccount, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchDevices requests a page of the full device list (POST
// /server/devices). An empty cursor starts from the beginning; limit 0
// lets the service choose.
func (c *Client) FetchDevices(ctx context.Context, account, cursor string, limit int) (*DevicePage, error) {
	var out DevicePage
	if err := c.call(ctx, account, http.MethodPost, PathFetchDevices, nil, fetchRequest{Cursor: cursor, Limit: limit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SyncDevices requests the changes since cursor (POST /devices/sync).
func (c *Client) SyncDevices(ctx context.Context, account, cursor string, limit int) (*DevicePage, error) {
	if cursor == "" {
		return nil, fmt.Errorf("%w: sync needs a cursor", ErrInvalid)
	}
	var out DevicePage
	if err := c.call(ctx, account, http.MethodPost, PathSyncDevices, nil, fetchRequest{Cursor: cursor, Limit: limit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeviceDetails fetches the current record of each serial (POST
// /devices). Unknown serials come back with a response_status of
// NOT_ACCESSIBLE.
func (c *Client) DeviceDetails(ctx context.Context, account string, serials []string) (map[string]Device, error) {
	if err := needSerials(serials); err != nil {
		return nil, err
	}
	var out DeviceDetailsResponse
	if err := c.call(ctx, account, http.MethodPost, PathDeviceDetails, nil, serialsRequest{Devices: serials}, &out); err != nil {
		return nil, err
	}
	for k, d := range out.Devices {
		d.normalise()
		out.Devices[k] = d
	}
	return out.Devices, nil
}

// DisownDevices tells Apple the organisation no longer owns the serials
// (POST /devices/disown) and returns the per-serial outcome.
func (c *Client) DisownDevices(ctx context.Context, account string, serials []string) (map[string]string, error) {
	if err := needSerials(serials); err != nil {
		return nil, err
	}
	var out DeviceStatuses
	if err := c.call(ctx, account, http.MethodPost, PathDisown, nil, serialsRequest{Devices: serials}, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// ActivationLock enables Activation Lock on one device (POST
// /device/activationlock).
func (c *Client) ActivationLock(ctx context.Context, account string, req ActivationLockRequest) (*ActivationLockResponse, error) {
	if req.Device == "" {
		return nil, fmt.Errorf("%w: activation lock needs a device serial", ErrInvalid)
	}
	var out ActivationLockResponse
	if err := c.call(ctx, account, http.MethodPost, PathActivationLck, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DefineProfile validates p locally with the rules Apple documents and
// creates it (POST /profile). The returned response carries the UUID
// Apple assigned; p.ProfileUUID is set to it as well.
func (c *Client) DefineProfile(ctx context.Context, account string, p *Profile) (*ProfileResponse, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var out ProfileResponse
	if err := c.call(ctx, account, http.MethodPost, PathProfile, nil, p, &out); err != nil {
		return nil, err
	}
	if out.ProfileUUID != "" {
		p.ProfileUUID = out.ProfileUUID
	}
	return &out, nil
}

// AssignOption tunes AssignProfile.
type AssignOption func(*assignOptions)

type assignOptions struct{ put bool }

// WithAssignPUT sends PUT instead of POST to /profile/devices, which some
// simulators expect; Apple documents POST.
func WithAssignPUT() AssignOption { return func(o *assignOptions) { o.put = true } }

// AssignProfile assigns the profile to the serials (POST /profile/devices,
// or PUT with WithAssignPUT). Apple advises at most 1000 serials per call.
func (c *Client) AssignProfile(ctx context.Context, account, profileUUID string, serials []string, opts ...AssignOption) (*AssignResponse, error) {
	if profileUUID == "" {
		return nil, fmt.Errorf("%w: assign needs a profile UUID", ErrInvalid)
	}
	if err := needSerials(serials); err != nil {
		return nil, err
	}
	var o assignOptions
	for _, opt := range opts {
		opt(&o)
	}
	method := http.MethodPost
	if o.put {
		method = http.MethodPut
	}
	var out AssignResponse
	if err := c.call(ctx, account, method, PathProfileDevs, nil, serialsRequest{ProfileUUID: profileUUID, Devices: serials}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveProfile clears the profile of the serials (DELETE /profile/devices).
func (c *Client) RemoveProfile(ctx context.Context, account string, serials []string) (map[string]string, error) {
	if err := needSerials(serials); err != nil {
		return nil, err
	}
	var out DeviceStatuses
	if err := c.call(ctx, account, http.MethodDelete, PathProfileDevs, nil, serialsRequest{Devices: serials}, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// FetchProfile reads a profile back (GET /profile?profile_uuid=).
func (c *Client) FetchProfile(ctx context.Context, account, profileUUID string) (*Profile, error) {
	if profileUUID == "" {
		return nil, fmt.Errorf("%w: fetch needs a profile UUID", ErrInvalid)
	}
	var out Profile
	if err := c.call(ctx, account, http.MethodGet, PathProfile, url.Values{"profile_uuid": {profileUUID}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AssignAccountDrivenEnrollmentDiscovery sets the MDM service discovery
// URL for account-driven enrollment (POST
// /account-driven-enrollment/profile).
func (c *Client) AssignAccountDrivenEnrollmentDiscovery(ctx context.Context, account, discoveryURL string) error {
	if discoveryURL == "" {
		return fmt.Errorf("%w: empty discovery URL", ErrInvalid)
	}
	return c.call(ctx, account, http.MethodPost, PathDiscovery, nil, discoveryProfile{MDMServiceDiscoveryURL: discoveryURL}, nil)
}

// FetchAccountDrivenEnrollmentDiscovery reads the discovery URL (GET
// /account-driven-enrollment/profile); *Error with Status 404 when none.
func (c *Client) FetchAccountDrivenEnrollmentDiscovery(ctx context.Context, account string) (string, error) {
	var out discoveryProfile
	if err := c.call(ctx, account, http.MethodGet, PathDiscovery, nil, nil, &out); err != nil {
		return "", err
	}
	return out.MDMServiceDiscoveryURL, nil
}

// RemoveAccountDrivenEnrollmentDiscovery clears the discovery URL (DELETE
// /account-driven-enrollment/profile).
func (c *Client) RemoveAccountDrivenEnrollmentDiscovery(ctx context.Context, account string) error {
	return c.call(ctx, account, http.MethodDelete, PathDiscovery, nil, nil, nil)
}

// BetaEnrollmentTokens lists the beta enrollment tokens (GET
// /os-beta-enrollment/tokens). ErrSeedForITOff wraps the 403
// APPLE_SEED_FOR_IT_TURNED_OFF answer.
func (c *Client) BetaEnrollmentTokens(ctx context.Context, account string) ([]BetaToken, error) {
	var out betaTokensResponse
	if err := c.call(ctx, account, http.MethodGet, PathBetaTokens, nil, nil, &out); err != nil {
		if codeIs(err, CodeSeedForITOff) {
			return nil, fmt.Errorf("%w: %w", ErrSeedForITOff, err)
		}
		return nil, err
	}
	if len(out.BetaEnrollmentTokens) > 0 {
		return out.BetaEnrollmentTokens, nil
	}
	return out.SeedBuildTokens, nil
}

func needSerials(serials []string) error {
	if len(serials) == 0 {
		return fmt.Errorf("%w: no device serials", ErrInvalid)
	}
	for _, s := range serials {
		if s == "" {
			return fmt.Errorf("%w: empty device serial", ErrInvalid)
		}
	}
	return nil
}
