// Package httpapi exposes the service layer over HTTP the way Apple devices
// expect: a check-in URL and a server URL that accept PUT requests carrying
// plists, identified by content type, plus the middlewares that extract the
// device identity certificate from TLS, a proxy header, or the
// Mdm-Signature header.
//
// # Why
//
// Apple devices speak a narrow HTTP dialect: PUT, a plist body, an
// identity proven by a client certificate or a detached CMS signature, and
// a small set of status codes with fixed meanings. Phase 2 of the plan of
// record needs handlers that map that dialect onto the typed service
// interfaces without leaking transport concerns into them. Handler routes
// by content type so both URLs can point at one path, as NanoMDM and
// MicroMDM deployments commonly do, and the certificate middlewares put the
// verified identity in the request context for the service to pin.
//
// The handlers never return 401: some Apple clients treat it as a reason to
// unenroll (decision record 0006). Unknown enrollments get 403, with
// Apple's ErrorUnrecognizedDevice body only when the deployment opts in.
// Enrollment profile delivery and the OTA profile service live in enroll,
// the DDM endpoints in the server/ddmadapter packages, and the reference server
// wiring in phase 8.
//
// # References
//
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0006: docs/research/decisions/0006-mdm-signature-verification.md
//   - Plan of record: docs/research/implementation_plan.md (phase 2)
//   - Threat model: docs/security/threat-model.md (/checkin and /connect rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-001 to E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-connections
//   - Schema: third_party/device-management/mdm/checkin/*.yaml
//   - Schema: third_party/device-management/mdm/errors/*.yaml
package httpapi
