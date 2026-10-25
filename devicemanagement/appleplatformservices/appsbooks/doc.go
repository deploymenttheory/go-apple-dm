// Package appsbooks implements Apple's Apps and Books version 2 licensing API
// for device/user apps and user books. It owns HTTP encoding, location identity,
// dynamic request limits, pagination, user lifecycle and notification decoding.
// Optional Workflow composes caller-owned atomic location and licensing-job persistence. The caller owns
// notification hosting, installation, reconciliation
// schedules and human acceptance of user invitations. No catalog metadata,
// subscriptions or automatic installation/uncertain mutation retry is included.
//
// # Design
//
// Start with New, ClientConfig and an explicit SetClientConfig for an unclaimed
// location. Keep the notification receiver available when configuring it: Apple
// sends TEST_NOTIFICATION before accepting the configuration. Use a separate
// Client for each content token/location and check errors on every call.
//
// AssetsQuery.Unlimited distinguishes true, false and omission. AssetsPage retains
// typed finite/sentinel and unlimited arrays plus original JSON. WalkAssets visits
// both representations with a page cap; use Stock to distinguish missing counts,
// finite availability and unlimited inventory. Availability is not licensing.
//
// NewWorkflow accepts a caller-owned state.Store and stable MDM ID. Register
// validates each location token through a read-only probe, preserving Apple's UID
// across renewal. Tokens, safe metadata and sensitive raw evidence occupy separate
// records. Load returns safe metadata; LoadEvidence requires privileged access.
// Configure reserves durable pending state before an explicit ownership write;
// Probe reconciles after the original operation has stopped; RecoverConfiguration
// supplies a replacement token when the pending operation's token has expired.
// Both recovery paths only read Apple configuration. Refresh publishes
// full assets and incremental assignment/user source pages and checkpoints only
// after all traversals succeed. Revisions fence old work after any local change.
//
// CreateJob persists explicit requests. SubmitJob reserves a durable location fence
// before attempting a POST once; uncertain or expired workers require ReconcileJob.
// RecoverLicensingToken validates token renewal while preserving uncertain intent.
// ReceiveNotification commits authenticated deduplication and target observations
// before the host may acknowledge. TEST_NOTIFICATION uses the same path. PollJob
// checks aggregate event type/counts; partial batches never establish job success.
// Jobs and raw callback evidence require privileged access and caller retention.
//
// Associate/CreateUsers return asynchronous Event IDs. Use authenticated
// notifications plus EventStatus to establish completion. Registered users need
// to associate an Apple Account before user-assigned content is usable. Books
// cannot be assigned to devices or reclaimed. Check the returned asset's
// DeviceAssignable and Revocable flags as well as Apple's per-task failures.
//
// # References
//
//   - Design decision: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0053-apps-and-books-licensing.md
//
//   - Apple management API: https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api
//
//   - Asset assignment and book restrictions: https://developer.apple.com/documentation/devicemanagement/managing-assets
//
//   - User registration and association: https://developer.apple.com/documentation/devicemanagement/managing-users
//
//   - Pagination and incremental queries: https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints
//
//   - Dynamic service limits: https://developer.apple.com/documentation/devicemanagement/service-config
//
//   - Notification authentication and delivery: https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications
//
//   - Repository guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/apps-and-books.md
package appsbooks
