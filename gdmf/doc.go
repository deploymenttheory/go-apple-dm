// Package gdmf reads Apple's software lookup service, the public catalog
// of operating system versions at https://gdmf.apple.com/v2/pmv, and
// answers "what is the latest version for this device".
//
// # Why
//
// The Automated Device Enrollment software update gate (decision record
// 0027) can require a device to update before it enrols. When policy says
// "latest", the server needs the current version and build for the
// device's model, and Apple publishes exactly that in the pmv document:
// per-platform asset sets carrying ProductVersion, Build, PostingDate,
// ExpirationDate, and SupportedDevices. Phase 6 of the plan of record uses
// this package from enroll/ade through the Lookup interface so the gate
// can be tested against gdmftest without the network.
//
// The client is small on purpose: one GET, a bounded body decoded with
// encoding/json/v2, a TTL cache that serves the last catalog when a
// refresh fails, and a numeric version comparison. It does not schedule
// updates, know about rapid security responses, or persist anything; a
// server that wants a shared cache wraps Lookup itself.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-011)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo (SOFTWARE_UPDATE_DEVICE_ID)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired
//   - Apple: https://support.apple.com/guide/deployment/use-mdm-to-deploy-software-updates-depafd2fad80/web
//   - Schema: third_party/device-management/mdm/errors/softwareupdate.required.yaml
package gdmf
