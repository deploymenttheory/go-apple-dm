// Package blueprint composes configurations, assets, management declarations
// and activations into declaration sets for declarative device management.
//
// # Design
//
// The compiler accepts source identifiers, generated or raw declaration payloads,
// configuration profile references and activations. It resolves asset references,
// validates payloads and predicates, and derives stable identifiers and canonical
// declarations. Compilation copies its inputs and performs no I/O. Callers supply
// verified metadata for uploaded configuration profiles.
//
// Blueprint source is a local convenience format. Devices receive Apple's
// standard declaration types, including ActivationSimple and LegacyProfile.
// Publication belongs to mdmprotocol/ddm, authoring persistence to
// server/blueprints, and configuration profile storage and delivery to
// server/configurationprofile. The appleplatformservices/axm package separately
// manages Blueprint resources hosted by Apple Business.
//
// # References
//
//   - Decision record 0055: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0055-blueprint-composition.md
//   - Decision record 0019: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0019-canonical-json-and-ddm-tokens.md
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Operations: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/blueprints.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/activationsimple
//   - Apple: https://developer.apple.com/documentation/devicemanagement/legacyprofile
//   - Schema: third_party/apple-device-management/current/declarative/declarations/**
package blueprint
