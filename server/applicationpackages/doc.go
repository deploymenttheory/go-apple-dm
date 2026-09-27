// Package applicationpackages hosts verified installer revisions for native MDM
// and DDM delivery through enrollment-scoped download grants.
//
// # Design
//
// Host uses the applications library for metadata, content and manifest integrity.
// Prepare validates the enrolled device's capabilities and creates a bounded grant
// for one immutable revision. Only the grant token's hash is stored. Fetch checks
// expiry, revocation, enrollment status and package availability on every request.
// Download URLs are bearer credentials and require HTTPS and access-log redaction.
//
// The reference server authorizes operators, dispatches the prepared MDM command
// or DDM publication and observes device results. Preparing or queuing a package
// does not establish installation. Revoke removes download access; removing a DDM
// assignment or uninstalling an application remains a separate device operation.
//
// # References
//
//   - Apple enterprise application installation: https://developer.apple.com/documentation/devicemanagement/installenterpriseapplicationcommand
//   - Apple declarative package schema: https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/package.yaml
package applicationpackages
