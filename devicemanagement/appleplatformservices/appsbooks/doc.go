// Package appsbooks implements Apple's Apps and Books version 2 licensing API
// for device/user apps and user books. It owns HTTP encoding, location identity,
// dynamic request limits, pagination, user lifecycle and notification decoding.
// The caller owns persistence, notification hosting, installation, reconciliation
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
// Associate/CreateUsers return asynchronous Event IDs. Use authenticated
// notifications plus EventStatus to establish completion. Registered users need
// to associate an Apple Account before user-assigned content is usable. Books
// cannot be assigned to devices or reclaimed. Check the returned asset's
// DeviceAssignable and Revocable flags as well as Apple's per-task failures.
//
// # References
//
//   - Apple management API: https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api
//   - Asset assignment and book restrictions: https://developer.apple.com/documentation/devicemanagement/managing-assets
//   - User registration and association: https://developer.apple.com/documentation/devicemanagement/managing-users
//   - Pagination and incremental queries: https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints
//   - Dynamic service limits: https://developer.apple.com/documentation/devicemanagement/service-config
//   - Notification authentication and delivery: https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications
//   - Repository guide: docs/operations/apps-and-books.md (relative to repository root)
package appsbooks
