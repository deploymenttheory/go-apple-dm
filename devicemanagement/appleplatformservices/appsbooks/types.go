package appsbooks

import "encoding/json/jsontext"

// Asset identifies a purchased app or book, including its pricing tier.
type Asset struct {
	AdamID       string `json:"adamId"`
	PricingParam string `json:"pricingParam"`
}

// AssetRecord contains Apple's counts and assignment restrictions.
type AssetRecord struct {
	Asset
	ProductType        string   `json:"productType"`
	DeviceAssignable   bool     `json:"deviceAssignable"`
	Revocable          bool     `json:"revocable"`
	AssignedCount      int64    `json:"assignedCount"`
	AvailableCount     int64    `json:"availableCount"`
	RetiredCount       int64    `json:"retiredCount"`
	TotalCount         int64    `json:"totalCount"`
	SupportedPlatforms []string `json:"supportedPlatforms"`
}

// Assignment identifies a device or user license assignment.
type Assignment struct {
	Asset
	ClientUserID string `json:"clientUserId,omitempty"`
	SerialNumber string `json:"serialNumber,omitempty"`
	IDHash       string `json:"idHash,omitempty"`
	UserStatus   string `json:"userStatus,omitempty"`
}

// User contains registration/association state. Registered is not Associated.
type User struct {
	ClientUserID string `json:"clientUserId"`
	Email        string `json:"email,omitempty"`
	IDHash       string `json:"idHash,omitempty"`
	InviteCode   string `json:"inviteCode,omitempty"`
	Status       string `json:"status"`
}

// RequestUser creates, updates or retires a user. Apple accepts managedAppleId
// for a Managed Apple Account; ordinary users accept an invitation themselves.
type RequestUser struct {
	ClientUserID   string `json:"clientUserId"`
	Email          string `json:"email,omitempty"`
	ManagedAppleID string `json:"managedAppleId,omitempty"`
}

// MDMInfo identifies the single MDM managing a location.
type MDMInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Metadata string `json:"metadata"`
}

// ResponseMeta accompanies authenticated responses. UID identifies the library;
// tokenExpirationDate may be omitted until expiration is within 15 days.
type ResponseMeta struct {
	UID                 string   `json:"uId"`
	MDMInfo             *MDMInfo `json:"mdmInfo,omitempty"`
	TokenExpirationDate string   `json:"tokenExpirationDate,omitempty"`
}

// Pagination uses Apple's nextPageIndex, whose absence marks the last page.
// Persist the FIRST page's VersionID only after consuming all pages successfully.
type Pagination struct {
	CurrentPageIndex int    `json:"currentPageIndex"`
	NextPageIndex    *int   `json:"nextPageIndex,omitempty"`
	Size             int    `json:"size"`
	TotalPages       int    `json:"totalPages"`
	VersionID        string `json:"versionId"`
}

// AssetsPage is a page of purchased app/book licenses. Unlimited asset responses
// are preserved as raw data because that separate API is outside this client.
type AssetsPage struct {
	ResponseMeta
	Pagination
	Assets          []AssetRecord  `json:"assets"`
	UnlimitedAssets jsontext.Value `json:"unlimitedAssets,omitempty"`
}

// AssignmentsPage is a full or incremental assignment page.
type AssignmentsPage struct {
	ResponseMeta
	Pagination
	Assignments []Assignment `json:"assignments"`
}

// UsersPage is a full or incremental user page. Retired and recreated users may
// share ClientUserID; only one may be active at a time.
type UsersPage struct {
	ResponseMeta
	Pagination
	Users []User `json:"users"`
}

// ManageAssetsRequest assigns or unassigns the assets to every listed target.
// Check AssetRecord.DeviceAssignable and Revocable before constructing requests.
// Books require users and cannot be revoked/reassigned. Apple enforces these
// restrictions; the client does not guess product type from an AdamID.
type ManageAssetsRequest struct {
	Assets        []Asset  `json:"assets"`
	ClientUserIDs []string `json:"clientUserIds,omitempty"`
	SerialNumbers []string `json:"serialNumbers,omitempty"`
}

// RevokeAssetsRequest revokes all revocable assets from the selected targets.
type RevokeAssetsRequest struct {
	ClientUserIDs []string `json:"clientUserIds,omitempty"`
	SerialNumbers []string `json:"serialNumbers,omitempty"`
}

// ManageUsersRequest is shared by create, update and retire.
type ManageUsersRequest struct {
	Users []RequestUser `json:"users"`
}

// Event acknowledges acceptance only, not successful licensing.
type Event struct {
	ResponseMeta
	EventID string `json:"eventId"`
}

// Status describes the aggregate asynchronous result, including partial failures.
type Status struct {
	ResponseMeta
	EventStatus  string    `json:"eventStatus"`
	EventType    string    `json:"eventType"`
	NumCompleted int64     `json:"numCompleted"`
	NumRequested int64     `json:"numRequested"`
	Failures     []Failure `json:"failures,omitempty"`
}

// Successful reports complete success; a partial or unknown result is false.
func (s Status) Successful() bool {
	return s.EventStatus == "COMPLETE" && s.NumRequested > 0 && s.NumCompleted == s.NumRequested &&
		len(s.Failures) == 0
}

// ServiceConfiguration contains dynamic limits/URLs. Unknown keys are retained.
type ServiceConfiguration struct {
	Limits            map[string]int    `json:"limits"`
	URLs              map[string]string `json:"urls"`
	NotificationTypes []string          `json:"notificationTypes"`
	ErrorCodes        []Failure         `json:"errorCodes"`
}

// ClientConfiguration includes notification credentials. Do not log this value.
type ClientConfiguration struct {
	ResponseMeta
	CountryISO2ACode            string   `json:"countryISO2ACode"`
	DefaultPlatform             string   `json:"defaultPlatform"`
	WebsiteURL                  string   `json:"websiteURL"`
	LocationName                string   `json:"locationName"`
	NotificationURL             string   `json:"notificationUrl"`
	NotificationAuthToken       string   `json:"notificationAuthToken"`
	SubscribedNotificationTypes []string `json:"subscribedNotificationTypes"`
}

// ClientConfigurationRequest explicitly replaces notification configuration.
// Apple sends TEST_NOTIFICATION before accepting a notification endpoint.
type ClientConfigurationRequest struct {
	MDMInfo               MDMInfo  `json:"mdmInfo"`
	NotificationURL       string   `json:"notificationUrl,omitempty"`
	NotificationAuthToken string   `json:"notificationAuthToken,omitempty"`
	NotificationTypes     []string `json:"notificationTypes"`
}
