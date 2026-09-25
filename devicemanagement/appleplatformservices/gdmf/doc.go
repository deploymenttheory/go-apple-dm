// Package gdmf reads Apple's software lookup catalog and selects operating
// system versions for devices.
//
// # Design
//
// The client fetches https://gdmf.apple.com/v2/pmv with bounded strict JSON
// decoding. A TTL cache can serve the last catalog when refresh fails. Numeric
// version comparison and the Lookup interface support the Automated Device
// Enrollment software-update gate.
//
// The package does not schedule updates or persist catalog data. Consumers
// needing shared caching can implement Lookup. A catalog lookup does not
// establish whether a particular device has installed an update.
//
// # Errors
//
// Every sentinel is an operator condition: the catalog is a dependency of the
// deployment, so a request, status, decode or size failure is Upstream and a
// device without an asset is NotFound.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-011)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo (SOFTWARE_UPDATE_DEVICE_ID)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deploying-software-updates-using-declarative-management
//   - Schema: third_party/apple-device-management/current/mdm/errors/softwareupdate.required.yaml
package gdmf
