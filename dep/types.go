package dep

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"time"
)

// DefaultProtocolVersion is the X-Server-Protocol-Version sent unless the
// account overrides it: the newest version the Device page declares keys
// for (MAC addresses, EID, IMEI, MEID, and replacement flags are "valid in
// X-Server-Protocol-Version 10 and later").
const DefaultProtocolVersion = 10

// Tokens is the JSON inside a decrypted server token file: the OAuth 1.0a
// consumer and access credentials Apple issues per MDM server.
//
//nolint:tagliatelle // keys mirror Apple's server token file
type Tokens struct {
	ConsumerKey       string     `json:"consumer_key"`
	ConsumerSecret    string     `json:"consumer_secret"`
	AccessToken       string     `json:"access_token"`
	AccessSecret      string     `json:"access_secret"`
	AccessTokenExpiry *time.Time `json:"access_token_expiry,omitzero"`
}

// Validate rejects a token set with an empty credential.
func (t Tokens) Validate() error {
	switch {
	case t.ConsumerKey == "":
		return fmt.Errorf("%w: empty consumer_key", ErrInvalid)
	case t.ConsumerSecret == "":
		return fmt.Errorf("%w: empty consumer_secret", ErrInvalid)
	case t.AccessToken == "":
		return fmt.Errorf("%w: empty access_token", ErrInvalid)
	case t.AccessSecret == "":
		return fmt.Errorf("%w: empty access_secret", ErrInvalid)
	}
	return nil
}

// Limit is the ranged page limit of one endpoint from the account detail.
//
//nolint:tagliatelle // keys mirror Apple's Limit object
type Limit struct {
	Default int            `json:"default,omitzero"`
	Maximum int            `json:"maximum,omitzero"`
	Extra   map[string]any `json:",embed"`
}

// URL is one entry of the account detail's urls array.
//
//nolint:tagliatelle // keys mirror Apple's Url object
type URL struct {
	URI        string         `json:"uri,omitzero"`
	HTTPMethod []string       `json:"http_method,omitzero"`
	Limit      *Limit         `json:"limit,omitzero"`
	Extra      map[string]any `json:",embed"`
}

// AccountDetail is the GET /account response.
//
//nolint:tagliatelle // keys mirror Apple's AccountDetail object
type AccountDetail struct {
	ServerName    string         `json:"server_name,omitzero"`
	ServerUUID    string         `json:"server_uuid,omitzero"`
	AdminID       string         `json:"admin_id,omitzero"`
	FacilitatorID string         `json:"facilitator_id,omitzero"`
	OrgName       string         `json:"org_name,omitzero"`
	OrgEmail      string         `json:"org_email,omitzero"`
	OrgPhone      string         `json:"org_phone,omitzero"`
	OrgAddress    string         `json:"org_address,omitzero"`
	OrgID         string         `json:"org_id,omitzero"`
	OrgIDHash     string         `json:"org_id_hash,omitzero"`
	OrgType       string         `json:"org_type,omitzero"`
	OrgVersion    string         `json:"org_version,omitzero"`
	URLs          []URL          `json:"urls,omitzero"`
	Extra         map[string]any `json:",embed"`
}

// Limits indexes the per-endpoint limits by URI.
func (a *AccountDetail) Limits() map[string]Limit {
	out := map[string]Limit{}
	for _, u := range a.URLs {
		if u.URI != "" && u.Limit != nil {
			out[u.URI] = *u.Limit
		}
	}
	return out
}

// Device is one device as the DEP service describes it: every key on the
// Device page. op_type and op_date are present on sync pages only;
// response_status on device detail answers only. Unknown keys round-trip
// through Extra.
//
//nolint:tagliatelle // keys mirror Apple's Device object
type Device struct {
	SerialNumber          string         `json:"serial_number"`
	Model                 string         `json:"model,omitzero"`
	Description           string         `json:"description,omitzero"`
	Color                 string         `json:"color,omitzero"`
	AssetTag              string         `json:"asset_tag,omitzero"`
	DeviceFamily          string         `json:"device_family,omitzero"`
	OS                    string         `json:"os,omitzero"`
	DeviceAssignedBy      string         `json:"device_assigned_by,omitzero"`
	DeviceAssignedDate    *time.Time     `json:"device_assigned_date,omitzero"`
	ProfileUUID           string         `json:"profile_uuid,omitzero"`
	ProfileStatus         string         `json:"profile_status,omitzero"`
	ProfileAssignTime     *time.Time     `json:"profile_assign_time,omitzero"`
	ProfilePushTime       *time.Time     `json:"profile_push_time,omitzero"`
	OpType                string         `json:"op_type,omitzero"`
	OpDate                *time.Time     `json:"op_date,omitzero"`
	MDMMigrationDeadline  *time.Time     `json:"mdm_migration_deadline,omitzero"`
	BluetoothMACAddress   string         `json:"bluetooth_mac_address,omitzero"`
	EthernetMACAddress    string         `json:"ethernet_mac_address,omitzero"`
	WifiMACAddress        string         `json:"wifi_mac_address,omitzero"`
	EID                   string         `json:"eid,omitzero"`
	IMEI                  []string       `json:"imei,omitzero"`
	MEID                  []string       `json:"meid,omitzero"`
	IsReplacementDevice   bool           `json:"is_replacement_device,omitzero"`
	ReleasedByReplacement bool           `json:"released_by_replacement,omitzero"`
	ResponseStatus        string         `json:"response_status,omitzero"`
	Extra                 map[string]any `json:",embed"`
}

// Op types on sync pages.
const (
	OpAdded    = "added"
	OpModified = "modified"
	OpDeleted  = "deleted"
)

// Profile status values on the Device page.
const (
	ProfileStatusEmpty    = "empty"
	ProfileStatusAssigned = "assigned"
	ProfileStatusPushed   = "pushed"
	ProfileStatusRemoved  = "removed"
)

// DevicePage is the FetchDeviceResponse of the fetch and sync endpoints.
//
//nolint:tagliatelle // keys mirror Apple's FetchDeviceResponse object
type DevicePage struct {
	Cursor       string         `json:"cursor"`
	Devices      []Device       `json:"devices,omitzero"`
	FetchedUntil *time.Time     `json:"fetched_until,omitzero"`
	MoreToFollow bool           `json:"more_to_follow"`
	Extra        map[string]any `json:",embed"`
}

// Profile is the DEP profile of the Profile page: every documented key,
// unknown keys through Extra. Booleans are pointers so an absent key and an
// explicit false stay distinct and DefineProfile after FetchProfile is
// byte-stable.
//
//nolint:tagliatelle // keys mirror Apple's Profile object
type Profile struct {
	ProfileUUID               string         `json:"profile_uuid,omitzero"`
	ProfileName               string         `json:"profile_name"`
	URL                       string         `json:"url"`
	OrgMagic                  string         `json:"org_magic"`
	AllowPairing              *bool          `json:"allow_pairing,omitzero"`
	AnchorCerts               []string       `json:"anchor_certs,omitzero"`
	AutoAdvanceSetup          *bool          `json:"auto_advance_setup,omitzero"`
	AwaitDeviceConfigured     *bool          `json:"await_device_configured,omitzero"`
	ConfigurationWebURL       string         `json:"configuration_web_url,omitzero"`
	Department                string         `json:"department,omitzero"`
	Devices                   []string       `json:"devices,omitzero"`
	DoNotUseProfileFromBackup *bool          `json:"do_not_use_profile_from_backup,omitzero"`
	IsMandatory               *bool          `json:"is_mandatory,omitzero"`
	IsMDMRemovable            *bool          `json:"is_mdm_removable,omitzero"`
	IsMultiUser               *bool          `json:"is_multi_user,omitzero"`
	IsReturnToService         *bool          `json:"is_return_to_service,omitzero"`
	IsSupervised              *bool          `json:"is_supervised,omitzero"`
	Language                  string         `json:"language,omitzero"`
	Region                    string         `json:"region,omitzero"`
	SkipSetupItems            []string       `json:"skip_setup_items,omitzero"`
	SupervisingHostCerts      []string       `json:"supervising_host_certs,omitzero"`
	SupportEmailAddress       string         `json:"support_email_address,omitzero"`
	SupportPhoneNumber        string         `json:"support_phone_number,omitzero"`
	Extra                     map[string]any `json:",embed"`
}

// ProfileResponse is the DefineProfileResponse: the UUID Apple assigned and
// the per-serial outcome for the devices named in the profile.
//
//nolint:tagliatelle // keys mirror Apple's DefineProfileResponse object
type ProfileResponse struct {
	ProfileUUID string            `json:"profile_uuid,omitzero"`
	Devices     map[string]string `json:"devices,omitzero"`
	Extra       map[string]any    `json:",embed"`
}

// AssignResponse is the AssignProfileResponse. RetryAfterSeconds is set
// (X-Server-Protocol-Version 10 and later) when at least one device is
// THROTTLED.
//
//nolint:tagliatelle // keys mirror Apple's AssignProfileResponse object
type AssignResponse struct {
	ProfileUUID       string            `json:"profile_uuid,omitzero"`
	Devices           map[string]string `json:"devices,omitzero"`
	RetryAfterSeconds int               `json:"retry_after_seconds,omitzero"`
	Extra             map[string]any    `json:",embed"`
}

// DeviceStatuses is the per-serial outcome map of the clear-profile and
// disown endpoints.
//
//nolint:tagliatelle // keys mirror Apple's DeviceStatusResponse object
type DeviceStatuses struct {
	Devices map[string]string `json:"devices,omitzero"`
	Extra   map[string]any    `json:",embed"`
}

// DeviceDetailsResponse is the DeviceListResponse of POST /devices.
//
//nolint:tagliatelle // keys mirror Apple's DeviceListResponse object
type DeviceDetailsResponse struct {
	Devices map[string]Device `json:"devices,omitzero"`
	Extra   map[string]any    `json:",embed"`
}

// ActivationLockRequest is the body of POST /device/activationlock.
//
//nolint:tagliatelle // keys mirror Apple's ActivationLockRequest object
type ActivationLockRequest struct {
	Device      string `json:"device"`
	EscrowKey   string `json:"escrow_key,omitzero"`
	LostMessage string `json:"lost_message,omitzero"`
}

// ActivationLockResponse is the ActivationLockStatusResponse.
//
//nolint:tagliatelle // keys mirror Apple's ActivationLockStatusResponse object
type ActivationLockResponse struct {
	SerialNumber   string         `json:"serial_number"`
	ResponseStatus string         `json:"response_status"`
	Extra          map[string]any `json:",embed"`
}

// BetaToken is one beta enrollment token from GET /os-beta-enrollment/tokens.
//
//nolint:tagliatelle // keys mirror Apple's SeedBuildToken object
type BetaToken struct {
	OS    string         `json:"os,omitzero"`
	Title string         `json:"title,omitzero"`
	Token string         `json:"token,omitzero"`
	Extra map[string]any `json:",embed"`
}

// betaTokensResponse is the GetSeedBuildTokenResponse.
//
//nolint:tagliatelle // keys mirror Apple's GetSeedBuildTokenResponse object
type betaTokensResponse struct {
	BetaEnrollmentTokens []BetaToken `json:"betaEnrollmentTokens,omitzero"`
	SeedBuildTokens      []BetaToken `json:"seedBuildTokens,omitzero"`
}

// discoveryProfile is the account-driven enrollment profile body.
//
//nolint:tagliatelle // keys mirror Apple's AccountDrivenEnrollmentProfileRequest object
type discoveryProfile struct {
	MDMServiceDiscoveryURL string `json:"mdm_service_discovery_url"`
}

// sessionResponse is the /session answer.
//
//nolint:tagliatelle // keys mirror Apple's /session response
type sessionResponse struct {
	AuthSessionToken string `json:"auth_session_token"`
}

// fetchRequest is the body of the fetch and sync endpoints.
type fetchRequest struct {
	Cursor string `json:"cursor,omitzero"`
	Limit  int    `json:"limit,omitzero"`
}

// serialsRequest names devices by serial for the details, disown, and
// profile endpoints.
//
//nolint:tagliatelle // keys mirror Apple's request objects
type serialsRequest struct {
	ProfileUUID string   `json:"profile_uuid,omitzero"`
	Devices     []string `json:"devices"`
}

// unmarshalTime parses Apple's ISO 8601 timestamps, treating an empty
// string as the zero time so a blank value never fails a page.
var unmarshalTime = json.UnmarshalFromFunc(func(dec *jsontext.Decoder, t *time.Time) error {
	v, err := dec.ReadValue()
	if err != nil {
		return err //nolint:wrapcheck // the decoder error is the contract of an unmarshaler
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	if s == "" {
		*t = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("timestamp %q: %w", s, err)
	}
	*t = parsed
	return nil
})

// unmarshalOptions decodes wire JSON with the tolerant timestamp parser.
var unmarshalOptions = json.WithUnmarshalers(unmarshalTime)

// Unmarshal decodes DEP JSON into v with the tolerant timestamp parser.
func Unmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v, unmarshalOptions); err != nil {
		return fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	return nil
}

// Marshal encodes v as DEP JSON: struct fields in declaration order, Extra
// keys sorted after them, so the same value always yields the same bytes.
func Marshal(v any) ([]byte, error) {
	out, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %w", ErrInvalid, err)
	}
	return out, nil
}

// normalise drops the zero timestamps a blank string produced so absent
// and empty keys look the same to callers.
func (d *Device) normalise() {
	for _, p := range []**time.Time{&d.DeviceAssignedDate, &d.ProfileAssignTime, &d.ProfilePushTime, &d.OpDate, &d.MDMMigrationDeadline} {
		if *p != nil && (*p).IsZero() {
			*p = nil
		}
	}
}

// Clone returns a deep copy.
func (d Device) Clone() Device {
	out := d
	out.IMEI = cloneStrings(d.IMEI)
	out.MEID = cloneStrings(d.MEID)
	out.Extra = cloneExtra(d.Extra)
	for _, p := range []**time.Time{&out.DeviceAssignedDate, &out.ProfileAssignTime, &out.ProfilePushTime, &out.OpDate, &out.MDMMigrationDeadline} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return out
}

// Clone returns a deep copy.
func (p Profile) Clone() Profile {
	out := p
	out.AnchorCerts = cloneStrings(p.AnchorCerts)
	out.Devices = cloneStrings(p.Devices)
	out.SkipSetupItems = cloneStrings(p.SkipSetupItems)
	out.SupervisingHostCerts = cloneStrings(p.SupervisingHostCerts)
	out.Extra = cloneExtra(p.Extra)
	for _, b := range []**bool{&out.AllowPairing, &out.AutoAdvanceSetup, &out.AwaitDeviceConfigured, &out.DoNotUseProfileFromBackup, &out.IsMandatory, &out.IsMDMRemovable, &out.IsMultiUser, &out.IsReturnToService, &out.IsSupervised} {
		if *b != nil {
			v := **b
			*b = &v
		}
	}
	return out
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

// cloneExtra deep-copies a decoded JSON tree through a re-encode, which is
// cheap for the handful of unknown keys a page carries.
func cloneExtra(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// Bool returns a pointer to b for the Profile flag fields.
func Bool(b bool) *bool { return &b }

// Time returns a pointer to t for the Device timestamp fields.
func Time(t time.Time) *time.Time { return &t }
