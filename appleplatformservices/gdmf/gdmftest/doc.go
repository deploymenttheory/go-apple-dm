// Package gdmftest fakes Apple's software lookup service for tests: a
// fixture catalog, an HTTP server that serves it, and an in-memory Lookup.
//
// # Why
//
// The software update gate in mdmprotocol/enroll/ade (decision record 0027) decides
// from the catalog which version a device must reach, and its tests must
// cover the catalog being unreachable or malformed as well as present.
// Talking to gdmf.apple.com from a test is neither reproducible nor
// polite, so this package serves a small fixed catalog over httptest and
// offers a Fake that answers from a map or fails on request. It is a test
// helper: exempt from the coverage gate and exercised by the gdmf and
// mdmprotocol/enroll/ade suites.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Apple: https://support.apple.com/guide/deployment/use-mdm-to-deploy-software-updates-depafd2fad80/web
package gdmftest
