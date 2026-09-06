// Package adetest builds the CMS-signed MachineInfo blobs a device sends
// during Automated Device Enrollment, from a test chain shaped like
// Apple's, and the three request forms that carry them.
//
// # Why
//
// The ade handler (decision record 0027) verifies a signature whose chain
// runs through an expired, SHA-1 signed intermediate, and reads the blob
// from a header, a query parameter, or a body. Its tests, the simulator's
// ADEEnroll, and the end-to-end scenarios all need blobs that exercise
// those paths without real device identities. NewChain issues a root, a
// "test iPhone Device CA" that expired in 2014 and signs with SHA-1, and
// a leaf; Sign wraps a MachineInfo with or without authenticated
// attributes; Request builds the header, query, and body forms. It is a
// test helper: exempt from the coverage gate and exercised by the suites
// that use it.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-011, E2E-018)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Schema: third_party/device-management/other/machineinfo.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package adetest
