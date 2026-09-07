// Package gdmftest supplies a fixture catalog, HTTP server and in-memory
// software Lookup for tests.
//
// # Design
//
// Software-update tests need reproducible catalog contents and controlled
// malformed or unreachable responses. The HTTP server exercises the real client;
// Fake answers from a map or returns a configured error. These helpers support
// gdmf and Automated Device Enrollment tests without live Apple service access.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Apple: https://support.apple.com/guide/deployment/use-mdm-to-deploy-software-updates-depafd2fad80/web
package gdmftest
