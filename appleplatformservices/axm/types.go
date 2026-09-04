package axm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// JSON:API resource type names.
const (
	TypeOrgDevices          = "orgDevices"
	TypeAppleCareCoverage   = "appleCareCoverage"
	TypeMDMDevices          = "mdmDevices"
	TypeMDMDeviceDetails    = "mdmDeviceDetails"
	TypeMDMServers          = "mdmServers"
	TypeOrgDeviceActivities = "orgDeviceActivities"
	TypeUsers               = "users"
	TypeUserGroups          = "userGroups"
	TypeOrganizationalUnits = "organizationalUnits"
	TypeApps                = "apps"
	TypePackages            = "packages"
	TypeConfigurations      = "configurations"
	TypeBlueprints          = "blueprints"
	TypeAuditEvents         = "auditEvents"
)

// Links carries the navigational links Apple attaches to documents,
// resources, and relationships. Unused members are empty.
type Links struct {
	Self    string `json:"self,omitempty"`
	Related string `json:"related,omitempty"`
	Include string `json:"include,omitempty"`
	First   string `json:"first,omitempty"`
	Next    string `json:"next,omitempty"`
}

// Linkage is a resource identifier: the {type, id} pair JSON:API uses in
// relationships.
type Linkage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Relationship is one entry of a resource's relationships object.
type Relationship struct {
	Links Links           `json:"links,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Meta is the meta object of a paged document.
type Meta struct {
	Paging Paging `json:"paging"`
}

// Paging is Apple's PagingInformation.Paging.
type Paging struct {
	Total      int    `json:"total,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// IncludedResource is one entry of a document's included array: the
// resource identifier plus its attributes left undecoded.
type IncludedResource struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
	Links      Links           `json:"links,omitempty"`
}

// StringList decodes either a JSON string or an array of strings. Apple
// documents imei, meid, and ethernetMacAddress as strings but returns
// arrays.
type StringList []string

// UnmarshalJSON implements json.Unmarshaler.
func (s *StringList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*s = nil
		return nil
	}
	if b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return fmt.Errorf("%w: string list: %w", ErrDecode, err)
		}
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("%w: string list: %w", ErrDecode, err)
	}
	*s = many
	return nil
}

// Extra holds the JSON members of an attributes object that the typed
// fields do not know. Apple adds attributes without a new API version, so
// the unknown ones are kept rather than dropped.
type Extra map[string]json.RawMessage

var knownKeys sync.Map // reflect.Type -> map[string]struct{}

// jsonKeys returns the JSON member names of t's tagged fields.
func jsonKeys(t reflect.Type) map[string]struct{} {
	if v, ok := knownKeys.Load(t); ok {
		return v.(map[string]struct{}) //nolint:forcetypeassert // only this type is stored
	}
	keys := map[string]struct{}{}
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			keys[name] = struct{}{}
		}
	}
	knownKeys.Store(t, keys)
	return keys
}

// decodeWithExtra decodes b into known (a pointer to a struct) and returns
// the members known does not declare.
func decodeWithExtra(b []byte, known any) (Extra, error) {
	if err := json.Unmarshal(b, known); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	keys := jsonKeys(reflect.TypeOf(known).Elem())
	for k := range all {
		if _, ok := keys[k]; ok {
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return Extra(all), nil
}

// OrgDeviceStatus is an organization device's assignment status.
type OrgDeviceStatus string

// Documented OrgDeviceStatus values; unknown values are preserved.
const (
	OrgDeviceStatusAssigned   OrgDeviceStatus = "ASSIGNED"
	OrgDeviceStatusUnassigned OrgDeviceStatus = "UNASSIGNED"
)

// PurchaseSourceType is how a device entered the organization.
type PurchaseSourceType string

// Documented PurchaseSourceType values.
const (
	PurchaseSourceApple         PurchaseSourceType = "APPLE"
	PurchaseSourceManuallyAdded PurchaseSourceType = "MANUALLY_ADDED"
	PurchaseSourceReseller      PurchaseSourceType = "RESELLER"
)

// MDMMigrationStatus is the state of a device's migration between
// device management services.
type MDMMigrationStatus string

// Documented MDMMigrationStatus values.
const (
	MDMMigrationRequested MDMMigrationStatus = "REQUESTED"
	MDMMigrationStarted   MDMMigrationStatus = "STARTED"
	MDMMigrationSuccess   MDMMigrationStatus = "SUCCESS"
	MDMMigrationFailed    MDMMigrationStatus = "FAILED"
)

// OrgDevice is a device registered to the organization.
type OrgDevice struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    OrgDeviceAttributes     `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// OrgDeviceAttributes are OrgDevice.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type OrgDeviceAttributes struct {
	SerialNumber                 string             `json:"serialNumber,omitempty"`
	AddedToOrgDateTime           time.Time          `json:"addedToOrgDateTime,omitzero"`
	ReleasedFromOrgDateTime      time.Time          `json:"releasedFromOrgDateTime,omitzero"`
	UpdatedDateTime              time.Time          `json:"updatedDateTime,omitzero"`
	DeviceModel                  string             `json:"deviceModel,omitempty"`
	ProductFamily                string             `json:"productFamily,omitempty"`
	ProductType                  string             `json:"productType,omitempty"`
	DeviceCapacity               string             `json:"deviceCapacity,omitempty"`
	PartNumber                   string             `json:"partNumber,omitempty"`
	OrderNumber                  string             `json:"orderNumber,omitempty"`
	Color                        string             `json:"color,omitempty"`
	Status                       OrgDeviceStatus    `json:"status,omitempty"`
	OrderDateTime                time.Time          `json:"orderDateTime,omitzero"`
	IMEI                         StringList         `json:"imei,omitempty"`
	MEID                         StringList         `json:"meid,omitempty"`
	EID                          string             `json:"eid,omitempty"`
	WiFiMACAddress               string             `json:"wifiMacAddress,omitempty"`
	BluetoothMACAddress          string             `json:"bluetoothMacAddress,omitempty"`
	EthernetMACAddress           StringList         `json:"ethernetMacAddress,omitempty"`
	PurchaseSourceID             string             `json:"purchaseSourceId,omitempty"`
	PurchaseSourceType           PurchaseSourceType `json:"purchaseSourceType,omitempty"`
	IsMDMMigrationCapable        bool               `json:"isMdmMigrationCapable,omitempty"`
	MDMMigrationStatus           MDMMigrationStatus `json:"mdmMigrationStatus,omitempty"`
	MDMMigrationDeadlineDateTime time.Time          `json:"mdmMigrationDeadlineDateTime,omitzero"`
	Extra                        Extra              `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *OrgDeviceAttributes) UnmarshalJSON(b []byte) error {
	type plain OrgDeviceAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// AppleCareCoverageStatus is the state of a coverage.
type AppleCareCoverageStatus string

// Documented AppleCareCoverageStatus values.
const (
	AppleCareCoverageActive   AppleCareCoverageStatus = "ACTIVE"
	AppleCareCoverageInactive AppleCareCoverageStatus = "INACTIVE"
)

// AppleCareCoveragePaymentType is how a coverage is paid for.
type AppleCareCoveragePaymentType string

// Documented AppleCareCoveragePaymentType values.
const (
	AppleCarePaymentABESubscription AppleCareCoveragePaymentType = "ABE_SUBSCRIPTION"
	AppleCarePaymentPaidUpFront     AppleCareCoveragePaymentType = "PAID_UP_FRONT"
	AppleCarePaymentSubscription    AppleCareCoveragePaymentType = "SUBSCRIPTION"
	AppleCarePaymentNone            AppleCareCoveragePaymentType = "NONE"
)

// AppleCareCoverage is one coverage resource of a device.
type AppleCareCoverage struct {
	Type       string                      `json:"type"`
	ID         string                      `json:"id"`
	Attributes AppleCareCoverageAttributes `json:"attributes"`
}

// AppleCareCoverageAttributes are AppleCareCoverage.Attributes.
type AppleCareCoverageAttributes struct {
	Status                 AppleCareCoverageStatus      `json:"status,omitempty"`
	PaymentType            AppleCareCoveragePaymentType `json:"paymentType,omitempty"`
	Description            string                       `json:"description,omitempty"`
	StartDateTime          time.Time                    `json:"startDateTime,omitzero"`
	EndDateTime            time.Time                    `json:"endDateTime,omitzero"`
	IsRenewable            bool                         `json:"isRenewable,omitempty"`
	IsCanceled             bool                         `json:"isCanceled,omitempty"`
	ContractCancelDateTime time.Time                    `json:"contractCancelDateTime,omitzero"`
	AgreementNumber        string                       `json:"agreementNumber,omitempty"`
	Extra                  Extra                        `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *AppleCareCoverageAttributes) UnmarshalJSON(b []byte) error {
	type plain AppleCareCoverageAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// MDMDevice is a device enrolled in Apple's built-in device management.
type MDMDevice struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    MDMDeviceAttributes     `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// MDMDeviceAttributes are MdmDevice.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type MDMDeviceAttributes struct {
	DeviceName     string `json:"deviceName,omitempty"`
	EnrolledUserID string `json:"enrolledUserId,omitempty"`
	ProductFamily  string `json:"productFamily,omitempty"`
	SerialNumber   string `json:"serialNumber,omitempty"`
}

// DeviceEraseStatus, DeviceLockStatus, and LostModeStatus are the
// documented status enums of MdmDeviceDetail.
type (
	DeviceEraseStatus string
	DeviceLockStatus  string
	LostModeStatus    string
)

// Documented values of the MdmDeviceDetail status enums.
const (
	DeviceNotErased  DeviceEraseStatus = "NOT_ERASED"
	DeviceErased     DeviceEraseStatus = "ERASED"
	DeviceLocked     DeviceLockStatus  = "LOCKED"
	DeviceUnlocked   DeviceLockStatus  = "UNLOCKED"
	LostModeEnabled  LostModeStatus    = "ENABLED"
	LostModeDisabled LostModeStatus    = "DISABLED"
)

// MDMDeviceDetail is the detailed view of an MDM-enrolled device.
type MDMDeviceDetail struct {
	Type       string                    `json:"type"`
	ID         string                    `json:"id"`
	Attributes MDMDeviceDetailAttributes `json:"attributes"`
	Links      Links                     `json:"links,omitempty"`
}

// MDMDeviceDetailAttributes are MdmDeviceDetail.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type MDMDeviceDetailAttributes struct {
	BluetoothMACAddress  string            `json:"bluetoothMacAddress,omitempty"`
	DeviceEraseStatus    DeviceEraseStatus `json:"deviceEraseStatus,omitempty"`
	DeviceLockStatus     DeviceLockStatus  `json:"deviceLockStatus,omitempty"`
	DeviceModel          string            `json:"deviceModel,omitempty"`
	DeviceName           string            `json:"deviceName,omitempty"`
	EthernetMACAddress   StringList        `json:"ethernetMacAddress,omitempty"`
	IMEI                 StringList        `json:"imei,omitempty"`
	IsFileVaultEnabled   bool              `json:"isFileVaultEnabled,omitempty"`
	IsFirewallEnabled    bool              `json:"isFirewallEnabled,omitempty"`
	LastCheckInDateTime  time.Time         `json:"lastCheckInDateTime,omitzero"`
	LostModeStatus       LostModeStatus    `json:"lostModeStatus,omitempty"`
	MEID                 StringList        `json:"meid,omitempty"`
	OSVersion            string            `json:"osVersion,omitempty"`
	Platform             string            `json:"platform,omitempty"`
	SerialNumber         string            `json:"serialNumber,omitempty"`
	StorageFreeCapacity  int64             `json:"storageFreeCapacity,omitempty"`
	StorageTotalCapacity int64             `json:"storageTotalCapacity,omitempty"`
	WiFiMACAddress       string            `json:"wifiMacAddress,omitempty"`
	Extra                Extra             `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *MDMDeviceDetailAttributes) UnmarshalJSON(b []byte) error {
	type plain MDMDeviceDetailAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// MDMServerType is the kind of device management service.
type MDMServerType string

// Documented MDMServerType values.
const (
	MDMServerTypeMDM               MDMServerType = "MDM"
	MDMServerTypeAppleConfigurator MDMServerType = "APPLE_CONFIGURATOR"
	MDMServerTypeAppleMDM          MDMServerType = "APPLE_MDM"
)

// MDMServerStatus is the operational status of a device management service.
type MDMServerStatus string

// Observed MDMServerStatus values.
const (
	MDMServerActive   MDMServerStatus = "ACTIVE"
	MDMServerInactive MDMServerStatus = "INACTIVE"
)

// MDMServer is a device management service in the organization. Its ID is
// 32 upper-case hexadecimal characters.
type MDMServer struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    MDMServerAttributes     `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// MDMServerAttributes are MdmServer.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type MDMServerAttributes struct {
	ServerName             string          `json:"serverName,omitempty"`
	ServerType             MDMServerType   `json:"serverType,omitempty"`
	EnableMDMDisownFlag    bool            `json:"enableMdmDisownFlag,omitempty"`
	DefaultProductFamilies []string        `json:"defaultProductFamilies,omitempty"`
	Status                 MDMServerStatus `json:"status,omitempty"`
	DeviceCount            int             `json:"deviceCount,omitempty"`
	LastConnectedDateTime  time.Time       `json:"lastConnectedDateTime,omitzero"`
	LastConnectedIP        string          `json:"lastConnectedIp,omitempty"`
	CreatedDateTime        time.Time       `json:"createdDateTime,omitzero"`
	UpdatedDateTime        time.Time       `json:"updatedDateTime,omitzero"`
	Extra                  Extra           `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *MDMServerAttributes) UnmarshalJSON(b []byte) error {
	type plain MDMServerAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// MDMServerCertificate is the push/identity certificate uploaded when a
// device management service is created.
type MDMServerCertificate struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// MDMServerCreateRequest is the body of Create an MdmServer.
type MDMServerCreateRequest struct {
	Data MDMServerCreateData `json:"data"`
}

// MDMServerCreateData is MdmServerCreateRequest.Data.
type MDMServerCreateData struct {
	Type       string                    `json:"type"`
	Attributes MDMServerCreateAttributes `json:"attributes"`
}

// MDMServerCreateAttributes are the creatable attributes; serverName and
// serverCertificate are required by Apple.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type MDMServerCreateAttributes struct {
	ServerName          string               `json:"serverName"`
	ServerCertificate   MDMServerCertificate `json:"serverCertificate"`
	EnableMDMDisownFlag bool                 `json:"enableMdmDisownFlag,omitempty"`
}

// MDMServerUpdateRequest is the body of Update an MdmServer; omitted
// attributes keep their value.
type MDMServerUpdateRequest struct {
	Data MDMServerUpdateData `json:"data"`
}

// MDMServerUpdateData is MdmServerUpdateRequest.Data.
type MDMServerUpdateData struct {
	Type       string                    `json:"type"`
	ID         string                    `json:"id"`
	Attributes MDMServerUpdateAttributes `json:"attributes"`
}

// MDMServerUpdateAttributes are the updatable attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type MDMServerUpdateAttributes struct {
	ServerName             string   `json:"serverName,omitempty"`
	EnableMDMDisownFlag    *bool    `json:"enableMdmDisownFlag,omitempty"`
	DefaultProductFamilies []string `json:"defaultProductFamilies,omitempty"`
}

// OrgDeviceActivityType names a device management activity.
type OrgDeviceActivityType string

// Documented OrgDeviceActivityType values. ReleaseDevices exists on the
// Apple Business API only.
const (
	ActivityAssignDevices                      OrgDeviceActivityType = "ASSIGN_DEVICES"
	ActivityUnassignDevices                    OrgDeviceActivityType = "UNASSIGN_DEVICES"
	ActivityAssignDevicesWithMigrationDeadline OrgDeviceActivityType = "ASSIGN_DEVICES_WITH_MDM_MIGRATION_DEADLINE"
	ActivityUpdateMigrationDeadline            OrgDeviceActivityType = "UPDATE_MDM_MIGRATION_DEADLINE"
	ActivityCancelMigration                    OrgDeviceActivityType = "CANCEL_MDM_MIGRATION"
	ActivityReleaseDevices                     OrgDeviceActivityType = "RELEASE_DEVICES"
)

// ActivityStatus is an activity's coarse status.
type ActivityStatus string

// Observed ActivityStatus values.
const (
	ActivityInProgress ActivityStatus = "IN_PROGRESS"
	ActivityCompleted  ActivityStatus = "COMPLETED"
	ActivityStopped    ActivityStatus = "STOPPED"
	ActivityFailed     ActivityStatus = "FAILED"
)

// ActivitySubStatus is an activity's fine-grained status.
type ActivitySubStatus string

// Observed ActivitySubStatus values.
const (
	ActivitySubmitted                     ActivitySubStatus = "SUBMITTED"
	ActivityPreProcessing                 ActivitySubStatus = "PRE_PROCESSING"
	ActivityPending                       ActivitySubStatus = "PENDING"
	ActivityProcessing                    ActivitySubStatus = "PROCESSING"
	ActivityPostProcessing                ActivitySubStatus = "POST_PROCESSING"
	ActivityStopping                      ActivitySubStatus = "STOPPING"
	ActivityCompletedWithSuccess          ActivitySubStatus = "COMPLETED_WITH_SUCCESS"
	ActivityCompletedWithError            ActivitySubStatus = "COMPLETED_WITH_ERROR"
	ActivityCompletedWithFailure          ActivitySubStatus = "COMPLETED_WITH_FAILURE"
	ActivityCompletedPostProcessingFailed ActivitySubStatus = "COMPLETED_POST_PROCESSING_FAILED"
)

// OrgDeviceActivity is one device management activity; its ID is a UUID.
type OrgDeviceActivity struct {
	Type       string                      `json:"type"`
	ID         string                      `json:"id"`
	Attributes OrgDeviceActivityAttributes `json:"attributes"`
	Links      Links                       `json:"links,omitempty"`
}

// OrgDeviceActivityAttributes are OrgDeviceActivity.Attributes.
// CompletedDateTime and DownloadURL are set once the activity completed;
// DownloadURL serves the activity log as CSV.
type OrgDeviceActivityAttributes struct {
	Status            ActivityStatus        `json:"status,omitempty"`
	SubStatus         ActivitySubStatus     `json:"subStatus,omitempty"`
	CreatedDateTime   time.Time             `json:"createdDateTime,omitzero"`
	CompletedDateTime time.Time             `json:"completedDateTime,omitzero"`
	DownloadURL       string                `json:"downloadUrl,omitempty"`
	ActivityType      OrgDeviceActivityType `json:"activityType,omitempty"`
	Extra             Extra                 `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *OrgDeviceActivityAttributes) UnmarshalJSON(b []byte) error {
	type plain OrgDeviceActivityAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// Terminal reports whether the activity reached a final status.
func (a *OrgDeviceActivity) Terminal() bool {
	switch a.Attributes.Status {
	case ActivityCompleted, ActivityStopped, ActivityFailed:
		return true
	case ActivityInProgress:
		return false
	}
	return false
}

// Succeeded reports whether the activity completed without any error.
func (a *OrgDeviceActivity) Succeeded() bool {
	return a.Attributes.Status == ActivityCompleted && a.Attributes.SubStatus == ActivityCompletedWithSuccess
}

// ActivityTypeMetadata carries the extra data of the migration activities.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type ActivityTypeMetadata struct {
	MDMMigrationDeadlineDateTime time.Time `json:"mdmMigrationDeadlineDateTime,omitzero"`
}

// OrgDeviceActivityCreateRequest is the body of Create an OrgDeviceActivity.
type OrgDeviceActivityCreateRequest struct {
	Data OrgDeviceActivityCreateData `json:"data"`
}

// OrgDeviceActivityCreateData is OrgDeviceActivityCreateRequest.Data.
type OrgDeviceActivityCreateData struct {
	Type          string                               `json:"type"`
	Attributes    OrgDeviceActivityCreateAttributes    `json:"attributes"`
	Relationships OrgDeviceActivityCreateRelationships `json:"relationships"`
}

// OrgDeviceActivityCreateAttributes are the activity's attributes.
type OrgDeviceActivityCreateAttributes struct {
	ActivityType         OrgDeviceActivityType `json:"activityType"`
	ActivityTypeMetadata *ActivityTypeMetadata `json:"activityTypeMetadata,omitempty"`
}

// OrgDeviceActivityCreateRelationships name the server and the devices.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type OrgDeviceActivityCreateRelationships struct {
	MDMServer *SingleLinkage `json:"mdmServer,omitempty"`
	Devices   ManyLinkage    `json:"devices"`
}

// SingleLinkage is a to-one relationship in a request.
type SingleLinkage struct {
	Data Linkage `json:"data"`
}

// ManyLinkage is a to-many relationship in a request.
type ManyLinkage struct {
	Data []Linkage `json:"data"`
}

// UserStatus is a user's account status.
type UserStatus string

// Documented UserStatus values.
const (
	UserNew                 UserStatus = "NEW"
	UserReleased            UserStatus = "RELEASED"
	UserActive              UserStatus = "ACTIVE"
	UserDeactivated         UserStatus = "DEACTIVATED"
	UserLocked              UserStatus = "LOCKED"
	UserLockedForSharedIPad UserStatus = "LOCKED_FOR_SHARED_IPAD"
)

// UserPhoneNumberType is the kind of a phone number.
type UserPhoneNumberType string

// Documented UserPhoneNumberType values.
const (
	PhoneWork   UserPhoneNumberType = "WORK"
	PhoneHome   UserPhoneNumberType = "HOME"
	PhoneMobile UserPhoneNumberType = "MOBILE"
)

// UserPhoneNumber is one of a user's phone numbers.
type UserPhoneNumber struct {
	PhoneNumber string              `json:"phoneNumber,omitempty"`
	Type        UserPhoneNumberType `json:"type,omitempty"`
}

// UserRoleOUMapping is a role held in an organizational unit.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type UserRoleOUMapping struct {
	RoleName string `json:"roleName,omitempty"`
	OUID     string `json:"ouId,omitempty"`
}

// User is a user of the organization.
type User struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Attributes UserAttributes `json:"attributes"`
	Links      Links          `json:"links,omitempty"`
}

// UserAttributes are User.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type UserAttributes struct {
	FirstName           string              `json:"firstName,omitempty"`
	LastName            string              `json:"lastName,omitempty"`
	MiddleName          string              `json:"middleName,omitempty"`
	Status              UserStatus          `json:"status,omitempty"`
	ManagedAppleAccount string              `json:"managedAppleAccount,omitempty"`
	IsExternalUser      bool                `json:"isExternalUser,omitempty"`
	RoleOUList          []UserRoleOUMapping `json:"roleOuList,omitempty"`
	Email               string              `json:"email,omitempty"`
	EmployeeNumber      string              `json:"employeeNumber,omitempty"`
	CostCenter          string              `json:"costCenter,omitempty"`
	Division            string              `json:"division,omitempty"`
	Department          string              `json:"department,omitempty"`
	JobTitle            string              `json:"jobTitle,omitempty"`
	StartDateTime       time.Time           `json:"startDateTime,omitzero"`
	CreatedDateTime     time.Time           `json:"createdDateTime,omitzero"`
	UpdatedDateTime     time.Time           `json:"updatedDateTime,omitzero"`
	PhoneNumbers        []UserPhoneNumber   `json:"phoneNumbers,omitempty"`
	Extra               Extra               `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *UserAttributes) UnmarshalJSON(b []byte) error {
	type plain UserAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// UserGroupType is how a group's membership is decided.
type UserGroupType string

// Documented UserGroupType values.
const (
	UserGroupStandard UserGroupType = "STANDARD"
	UserGroupSmart    UserGroupType = "SMART"
)

// UserGroupStatus is a group's status.
type UserGroupStatus string

// UserGroupActive is the documented UserGroupStatus value.
const UserGroupActive UserGroupStatus = "ACTIVE"

// UserGroup is a group of users.
type UserGroup struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    UserGroupAttributes     `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// UserGroupAttributes are UserGroup.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type UserGroupAttributes struct {
	OUID             string          `json:"ouId,omitempty"`
	Name             string          `json:"name,omitempty"`
	Type             UserGroupType   `json:"type,omitempty"`
	TotalMemberCount int             `json:"totalMemberCount,omitempty"`
	Status           UserGroupStatus `json:"status,omitempty"`
	CreatedDateTime  time.Time       `json:"createdDateTime,omitzero"`
	UpdatedDateTime  time.Time       `json:"updatedDateTime,omitzero"`
	Extra            Extra           `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *UserGroupAttributes) UnmarshalJSON(b []byte) error {
	type plain UserGroupAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// OrganizationalUnit is a location or department of the organization.
type OrganizationalUnit struct {
	Type          string                       `json:"type"`
	ID            string                       `json:"id"`
	Attributes    OrganizationalUnitAttributes `json:"attributes"`
	Relationships map[string]Relationship      `json:"relationships,omitempty"`
	Links         Links                        `json:"links,omitempty"`
}

// OrganizationalUnitAttributes are OrganizationalUnit.Attributes.
type OrganizationalUnitAttributes struct {
	Name            string    `json:"name,omitempty"`
	Description     string    `json:"description,omitempty"`
	CreatedDateTime time.Time `json:"createdDateTime,omitzero"`
	UpdatedDateTime time.Time `json:"updatedDateTime,omitzero"`
	Extra           Extra     `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *OrganizationalUnitAttributes) UnmarshalJSON(b []byte) error {
	type plain OrganizationalUnitAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// SupportedOS is an operating system an app supports.
type SupportedOS string

// Documented SupportedOS values.
const (
	SupportedOSiOS         SupportedOS = "SUPPORTED_OS_IOS"
	SupportedOSiPadOS      SupportedOS = "SUPPORTED_OS_IPADOS"
	SupportedOSmacOS       SupportedOS = "SUPPORTED_OS_MACOS"
	SupportedOStvOS        SupportedOS = "SUPPORTED_OS_TVOS"
	SupportedOSwatchOS     SupportedOS = "SUPPORTED_OS_WATCHOS"
	SupportedOSvisionOS    SupportedOS = "SUPPORTED_OS_VISIONOS"
	SupportedOSUnspecified SupportedOS = "SUPPORTED_OS_UNSPECIFIED"
)

// App is an app available to the built-in device management.
type App struct {
	Type       string        `json:"type"`
	ID         string        `json:"id"`
	Attributes AppAttributes `json:"attributes"`
	Links      Links         `json:"links,omitempty"`
}

// AppAttributes are App.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type AppAttributes struct {
	Name        string        `json:"name,omitempty"`
	BundleID    string        `json:"bundleId,omitempty"`
	WebsiteURL  string        `json:"websiteUrl,omitempty"`
	Version     string        `json:"version,omitempty"`
	SupportedOS []SupportedOS `json:"supportedOS,omitempty"`
	IsCustomApp bool          `json:"isCustomApp,omitempty"`
	AppStoreURL string        `json:"appStoreUrl,omitempty"`
	Extra       Extra         `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *AppAttributes) UnmarshalJSON(b []byte) error {
	type plain AppAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// Package is an installer package of the built-in device management.
type Package struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	Attributes PackageAttributes `json:"attributes"`
	Links      Links             `json:"links,omitempty"`
}

// PackageAttributes are Package.Attributes.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type PackageAttributes struct {
	Name            string    `json:"name,omitempty"`
	URL             string    `json:"url,omitempty"`
	Hash            string    `json:"hash,omitempty"`
	BundleIDs       []string  `json:"bundleIds,omitempty"`
	Description     string    `json:"description,omitempty"`
	Version         string    `json:"version,omitempty"`
	CreatedDateTime time.Time `json:"createdDateTime,omitzero"`
	UpdatedDateTime time.Time `json:"updatedDateTime,omitzero"`
	Extra           Extra     `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *PackageAttributes) UnmarshalJSON(b []byte) error {
	type plain PackageAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// ConfigurationType is the kind of a configuration; only CustomSetting can
// be created or updated through the API.
type ConfigurationType string

// Documented ConfigurationType values.
const (
	ConfigurationCustomSetting            ConfigurationType = "CUSTOM_SETTING"
	ConfigurationAirDrop                  ConfigurationType = "AIR_DROP"
	ConfigurationAirPrint                 ConfigurationType = "AIR_PRINT"
	ConfigurationAppAccess                ConfigurationType = "APP_ACCESS"
	ConfigurationAppleIntelligenceSiri    ConfigurationType = "APPLE_INTELLIGENCE_SIRI"
	ConfigurationApplicationLayerFirewall ConfigurationType = "APPLICATION_LAYER_FIREWALL"
	ConfigurationAuthenticationScreenLock ConfigurationType = "AUTHENTICATION_SCREEN_LOCK"
	ConfigurationCertificate              ConfigurationType = "CERTIFICATE"
	ConfigurationConferenceRoomDisplay    ConfigurationType = "CONFERENCE_ROOM_DISPLAY"
	ConfigurationContentCaching           ConfigurationType = "CONTENT_CACHING"
	ConfigurationCustomProfile            ConfigurationType = "CUSTOM_PROFILE"
	ConfigurationDataManagement           ConfigurationType = "DATA_MANAGEMENT"
	ConfigurationEnergySaver              ConfigurationType = "ENERGY_SAVER"
	ConfigurationFileVault                ConfigurationType = "FILE_VAULT"
	ConfigurationGatekeeper               ConfigurationType = "GATE_KEEPER"
	ConfigurationICloud                   ConfigurationType = "ICLOUD"
	ConfigurationLoginWindow              ConfigurationType = "LOGIN_WINDOW"
	ConfigurationMediaManagement          ConfigurationType = "MEDIA_MANAGEMENT"
	ConfigurationSoftwareUpdate           ConfigurationType = "SOFTWARE_UPDATE"
	ConfigurationVPN                      ConfigurationType = "VPN"
	ConfigurationWebClip                  ConfigurationType = "WEB_CLIP"
	ConfigurationWebFilter                ConfigurationType = "WEB_FILTER"
	ConfigurationWiFi                     ConfigurationType = "WIFI"
)

// ConfigurationPlatform is a platform a configuration targets.
type ConfigurationPlatform string

// Documented ConfigurationPlatform values.
const (
	PlatformMacOS    ConfigurationPlatform = "PLATFORM_MACOS"
	PlatformIOS      ConfigurationPlatform = "PLATFORM_IOS"
	PlatformTVOS     ConfigurationPlatform = "PLATFORM_TVOS"
	PlatformVisionOS ConfigurationPlatform = "PLATFORM_VISIONOS"
)

// CustomSettingsValues is the profile of a CUSTOM_SETTING configuration.
// ConfigurationProfile is the profile XML, base64-encoded on the wire.
type CustomSettingsValues struct {
	ConfigurationProfile string `json:"configurationProfile,omitempty"`
	Filename             string `json:"filename,omitempty"`
}

// Configuration is a configuration of the built-in device management.
type Configuration struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    ConfigurationAttributes `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// ConfigurationAttributes are ConfigurationCommon plus
// ConfigurationCustomSetting. CustomSettingsValues is null in list
// responses.
type ConfigurationAttributes struct {
	Type                   ConfigurationType       `json:"type,omitempty"`
	Name                   string                  `json:"name,omitempty"`
	ConfiguredForPlatforms []ConfigurationPlatform `json:"configuredForPlatforms,omitempty"`
	CustomSettingsValues   *CustomSettingsValues   `json:"customSettingsValues,omitempty"`
	CreatedDateTime        time.Time               `json:"createdDateTime,omitzero"`
	UpdatedDateTime        time.Time               `json:"updatedDateTime,omitzero"`
	Extra                  Extra                   `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *ConfigurationAttributes) UnmarshalJSON(b []byte) error {
	type plain ConfigurationAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// ConfigurationCreateRequest is the body of Create a Configuration.
type ConfigurationCreateRequest struct {
	Data ConfigurationCreateData `json:"data"`
}

// ConfigurationCreateData is ConfigurationCreateRequest.Data.
type ConfigurationCreateData struct {
	Type       string                        `json:"type"`
	Attributes ConfigurationCreateAttributes `json:"attributes"`
}

// ConfigurationCreateAttributes are the creatable attributes;
// configurationProfile is required, the type must be CUSTOM_SETTING.
type ConfigurationCreateAttributes struct {
	Type                   ConfigurationType       `json:"type"`
	Name                   string                  `json:"name"`
	ConfiguredForPlatforms []ConfigurationPlatform `json:"configuredForPlatforms,omitempty"`
	CustomSettingsValues   CustomSettingsValues    `json:"customSettingsValues"`
}

// ConfigurationUpdateRequest is the body of Update a Configuration.
type ConfigurationUpdateRequest struct {
	Data ConfigurationUpdateData `json:"data"`
}

// ConfigurationUpdateData is ConfigurationUpdateRequest.Data.
type ConfigurationUpdateData struct {
	Type       string                        `json:"type"`
	ID         string                        `json:"id"`
	Attributes ConfigurationUpdateAttributes `json:"attributes"`
}

// ConfigurationUpdateAttributes are the updatable attributes; at least one
// must be set.
type ConfigurationUpdateAttributes struct {
	Name                   string                  `json:"name,omitempty"`
	ConfiguredForPlatforms []ConfigurationPlatform `json:"configuredForPlatforms,omitempty"`
	CustomSettingsValues   *CustomSettingsValues   `json:"customSettingsValues,omitempty"`
}

// BlueprintStatus is a blueprint's status.
type BlueprintStatus string

// Documented BlueprintStatus values.
const (
	BlueprintActive      BlueprintStatus = "ACTIVE"
	BlueprintToBeDeleted BlueprintStatus = "TO_BE_DELETED"
)

// BlueprintRelationship names one of a blueprint's six relationships.
type BlueprintRelationship string

// The blueprint relationships.
const (
	BlueprintApps           BlueprintRelationship = "apps"
	BlueprintConfigurations BlueprintRelationship = "configurations"
	BlueprintPackages       BlueprintRelationship = "packages"
	BlueprintOrgDevices     BlueprintRelationship = "orgDevices"
	BlueprintUsers          BlueprintRelationship = "users"
	BlueprintUserGroups     BlueprintRelationship = "userGroups"
)

// BlueprintRelationships lists every blueprint relationship in Apple's
// order.
var BlueprintRelationships = []BlueprintRelationship{
	BlueprintApps, BlueprintConfigurations, BlueprintPackages,
	BlueprintOrgDevices, BlueprintUsers, BlueprintUserGroups,
}

// Blueprint is a blueprint of the built-in device management. Included
// holds the related resources a request asked for with include.
type Blueprint struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    BlueprintAttributes     `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
	Included      []IncludedResource      `json:"-"`
}

// BlueprintAttributes are Blueprint.Attributes.
type BlueprintAttributes struct {
	Name                string          `json:"name,omitempty"`
	Description         string          `json:"description,omitempty"`
	Status              BlueprintStatus `json:"status,omitempty"`
	AppLicenseDeficient bool            `json:"appLicenseDeficient,omitempty"`
	CreatedDateTime     time.Time       `json:"createdDateTime,omitzero"`
	UpdatedDateTime     time.Time       `json:"updatedDateTime,omitzero"`
	Extra               Extra           `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping unknown members in Extra.
func (a *BlueprintAttributes) UnmarshalJSON(b []byte) error {
	type plain BlueprintAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.Extra = extra
	return err
}

// BlueprintRequestRelationships are the relationships a create or update
// request sets, keyed by relationship name.
type BlueprintRequestRelationships map[BlueprintRelationship]ManyLinkage

// BlueprintCreateRequest is the body of Create a Blueprint.
type BlueprintCreateRequest struct {
	Data BlueprintCreateData `json:"data"`
}

// BlueprintCreateData is BlueprintCreateRequest.Data.
type BlueprintCreateData struct {
	Type          string                        `json:"type"`
	Attributes    BlueprintCreateAttributes     `json:"attributes"`
	Relationships BlueprintRequestRelationships `json:"relationships,omitempty"`
}

// BlueprintCreateAttributes are the creatable attributes; name is required.
type BlueprintCreateAttributes struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// BlueprintUpdateRequest is the body of Update a Blueprint.
type BlueprintUpdateRequest struct {
	Data BlueprintUpdateData `json:"data"`
}

// BlueprintUpdateData is BlueprintUpdateRequest.Data.
type BlueprintUpdateData struct {
	Type          string                        `json:"type"`
	ID            string                        `json:"id"`
	Attributes    *BlueprintUpdateAttributes    `json:"attributes,omitempty"`
	Relationships BlueprintRequestRelationships `json:"relationships,omitempty"`
}

// BlueprintUpdateAttributes are the updatable attributes.
type BlueprintUpdateAttributes struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// LinkagesRequest is the body of the relationship add and remove endpoints.
type LinkagesRequest struct {
	Data []Linkage `json:"data"`
}

// AuditEventType, AuditEventCategory, AuditEventActorType,
// AuditEventSubjectType, and AuditEventOutcome are the audit event enums.
type (
	AuditEventType        string
	AuditEventCategory    string
	AuditEventActorType   string
	AuditEventSubjectType string
	AuditEventOutcome     string
)

// Documented AuditEventType values.
const (
	AuditDeviceAddedToOrg                       AuditEventType = "DEVICE_ADDED_TO_ORG"
	AuditDeviceRemovedFromOrg                   AuditEventType = "DEVICE_REMOVED_FROM_ORG"
	AuditDeviceAssignedToServer                 AuditEventType = "DEVICE_ASSIGNED_TO_SERVER"
	AuditDeviceUnassignedFromServer             AuditEventType = "DEVICE_UNASSIGNED_FROM_SERVER"
	AuditSubjectHasICloudStoragePurchaseAdded   AuditEventType = "SUBJECT_HAS_ICLOUD_STORAGE_PURCHASE_ADDED"
	AuditSubjectHasICloudStoragePurchaseRemoved AuditEventType = "SUBJECT_HAS_ICLOUD_STORAGE_PURCHASE_REMOVED"
	AuditSubjectHasAppleCarePurchaseAdded       AuditEventType = "SUBJECT_HAS_APPLECARE_PURCHASE_ADDED"
	AuditSubjectHasAppleCarePurchaseRemoved     AuditEventType = "SUBJECT_HAS_APPLECARE_PURCHASE_REMOVED"
	AuditDeviceIsErased                         AuditEventType = "DEVICE_IS_ERASED"
	AuditConfigSettingsCreated                  AuditEventType = "CONFIG_SETTINGS_CREATED"
	AuditConfigSettingsUpdated                  AuditEventType = "CONFIG_SETTINGS_UPDATED"
	AuditConfigSettingsDeleted                  AuditEventType = "CONFIG_SETTINGS_DELETED"
	AuditCollectionCreated                      AuditEventType = "COLLECTION_CREATED"
	AuditCollectionUpdated                      AuditEventType = "COLLECTION_UPDATED"
	AuditCollectionDeleted                      AuditEventType = "COLLECTION_DELETED"
	AuditSubscriptionCreated                    AuditEventType = "SUBSCRIPTION_CREATED"
	AuditSubscriptionUpdated                    AuditEventType = "SUBSCRIPTION_UPDATED"
	AuditSubscriptionDeleted                    AuditEventType = "SUBSCRIPTION_DELETED"
	AuditAccountRoleLocationChanged             AuditEventType = "ACCOUNT_ROLE_LOCATION_CHANGED"
	AuditAccountAdded                           AuditEventType = "ACCOUNT_ADDED"
	AuditAccountDeleted                         AuditEventType = "ACCOUNT_DELETED"
	AuditExternalAccountAssociated              AuditEventType = "EXTERNAL_ACCOUNT_ASSOCIATED"
	AuditExternalAccountDisassociated           AuditEventType = "EXTERNAL_ACCOUNT_DISASSOCIATED"
	AuditDomainAdded                            AuditEventType = "DOMAIN_ADDED"
	AuditDomainRemoved                          AuditEventType = "DOMAIN_REMOVED"
	AuditDomainVerified                         AuditEventType = "DOMAIN_VERIFIED"
	AuditAPIAccountCreatedWithKey               AuditEventType = "API_ACCOUNT_CREATED_WITH_KEY"
	AuditAPIAccountCreatedWithoutKey            AuditEventType = "API_ACCOUNT_CREATED_WITHOUT_KEY"
	AuditAPIAccountDeleted                      AuditEventType = "API_ACCOUNT_DELETED"
	AuditAPIAccountKeyRevoked                   AuditEventType = "API_ACCOUNT_KEY_REVOKED"
	AuditAPIAccountKeyGenerated                 AuditEventType = "API_ACCOUNT_KEY_GENERATED"
	AuditAPIAccountRoleLocationChanged          AuditEventType = "API_ACCOUNT_ROLE_LOCATION_CHANGED"
	AuditAPIAccountNameChanged                  AuditEventType = "API_ACCOUNT_NAME_CHANGED"
)

// Documented values of the other audit event enums.
const (
	AuditCategoryOrganization     AuditEventCategory = "ORGANIZATION"
	AuditCategoryAccountActivity  AuditEventCategory = "ACCOUNT_ACTIVITY"
	AuditCategoryDeviceInventory  AuditEventCategory = "DEVICE_INVENTORY"
	AuditCategoryPurchasing       AuditEventCategory = "PURCHASING"
	AuditCategoryDeviceManagement AuditEventCategory = "DEVICE_MANAGEMENT"

	AuditActorUser    AuditEventActorType = "USER"
	AuditActorAPIUser AuditEventActorType = "API_USER"
	AuditActorSystem  AuditEventActorType = "SYSTEM"

	AuditSubjectOrganization            AuditEventSubjectType = "ORGANIZATION"
	AuditSubjectUser                    AuditEventSubjectType = "USER"
	AuditSubjectLocation                AuditEventSubjectType = "LOCATION"
	AuditSubjectDevice                  AuditEventSubjectType = "DEVICE"
	AuditSubjectCollection              AuditEventSubjectType = "COLLECTION"
	AuditSubjectDeviceManagementSetting AuditEventSubjectType = "DEVICE_MANAGEMENT_SETTING"
	AuditSubjectSubscription            AuditEventSubjectType = "SUBSCRIPTION"
	AuditSubjectDomain                  AuditEventSubjectType = "DOMAIN"
	AuditSubjectAPIUser                 AuditEventSubjectType = "API_USER"

	AuditOutcomeSuccess AuditEventOutcome = "SUCCESS"
	AuditOutcomeFailure AuditEventOutcome = "FAILURE"
)

// AuditEvent is one audit event; attributes are polymorphic by type.
type AuditEvent struct {
	Type       string               `json:"type"`
	ID         string               `json:"id"`
	Attributes AuditEventAttributes `json:"attributes"`
}

// AuditEventAttributes are AuditEventCommonAttributes plus the
// type-specific eventData member, kept in EventData under its key
// (EventDataPropertyKey names the one that applies).
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type AuditEventAttributes struct {
	EventDateTime        time.Time             `json:"eventDateTime,omitzero"`
	Type                 AuditEventType        `json:"type,omitempty"`
	Category             AuditEventCategory    `json:"category,omitempty"`
	ActorType            AuditEventActorType   `json:"actorType,omitempty"`
	ActorID              string                `json:"actorId,omitempty"`
	ActorName            string                `json:"actorName,omitempty"`
	SubjectType          AuditEventSubjectType `json:"subjectType,omitempty"`
	SubjectID            string                `json:"subjectId,omitempty"`
	SubjectName          string                `json:"subjectName,omitempty"`
	Outcome              AuditEventOutcome     `json:"outcome,omitempty"`
	GroupID              string                `json:"groupId,omitempty"`
	EventDataPropertyKey string                `json:"eventDataPropertyKey,omitempty"`
	EventData            Extra                 `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler, keeping the event data members
// in EventData.
func (a *AuditEventAttributes) UnmarshalJSON(b []byte) error {
	type plain AuditEventAttributes
	extra, err := decodeWithExtra(b, (*plain)(a))
	a.EventData = extra
	return err
}

// Data decodes the event data named by EventDataPropertyKey into v.
// ErrNoEventData is returned when the event carries none.
func (a *AuditEventAttributes) Data(v any) error {
	raw, ok := a.EventData[a.EventDataPropertyKey]
	if !ok || a.EventDataPropertyKey == "" {
		return fmt.Errorf("%w: %q", ErrNoEventData, a.EventDataPropertyKey)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDecode, a.EventDataPropertyKey, err)
	}
	return nil
}

// AuditEventDeviceAddedToOrg is the event data of DEVICE_ADDED_TO_ORG.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type AuditEventDeviceAddedToOrg struct {
	SerialNumber       string             `json:"serialNumber,omitempty"`
	PurchaseSourceType PurchaseSourceType `json:"purchaseSourceType,omitempty"`
	PurchaseSourceID   string             `json:"purchaseSourceId,omitempty"`
}

// AuditEventDeviceRemovedFromOrg is the event data of DEVICE_REMOVED_FROM_ORG.
//
//nolint:tagliatelle // tags mirror Apple's JSON member names
type AuditEventDeviceRemovedFromOrg struct {
	SerialNumber      string `json:"serialNumber,omitempty"`
	ReleaseEntityID   string `json:"releaseEntityId,omitempty"`
	ReleaseEntityType string `json:"releaseEntityType,omitempty"`
}

// AuditEventDeviceAssignedToServer is the event data of DEVICE_ASSIGNED_TO_SERVER.
type AuditEventDeviceAssignedToServer struct {
	SerialNumber     string `json:"serialNumber,omitempty"`
	TargetServerName string `json:"targetServerName,omitempty"`
}

// AuditEventDeviceUnassignedFromServer is the event data of
// DEVICE_UNASSIGNED_FROM_SERVER.
type AuditEventDeviceUnassignedFromServer struct {
	SerialNumber string `json:"serialNumber,omitempty"`
}
