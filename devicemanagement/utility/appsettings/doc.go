// Package appsettings constructs app controls and privacy defaults for Apple's
// App Settings declaration.
//
// # Design
//
// Constructors return generated schema/ddm payload types and validate them
// against an explicit operating-system version, channel and enrollment context.
// App lists use bundle identifiers. Binary rules use caller-selected CDHash,
// team and signing identifiers from utility/appidentity reports. Every
// architecture must supply the selected identity; identical rules are
// deduplicated. Optional path and signing-state constraints narrow each rule.
//
// Privacy defaults use generated permission records and an organization
// justification. The builder composes bundle identifiers and designated
// requirements for macOS, and uses bundle identifiers alone for iOS. Composition
// checks delimiter safety; Apple's client evaluates the requirement. Generated
// validation supplies platform, version, permission-value and scope constraints.
//
// Construction performs no I/O and does not choose a matching policy. Empty app
// lists are rejected because generated serialization omits empty arrays. Callers
// supply identity reports from a trusted source and use mdmprotocol/ddm or its
// blueprint package to wrap and publish the resulting payloads.
//
// # References
//
//   - Utility guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/utility/README.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Apple App Settings schema: https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/app.settings.yaml
//   - Schema: third_party/apple-device-management/current/declarative/declarations/configurations/app.settings.yaml
package appsettings
