// Package blueprints saves Blueprint specifications, compiles them into DDM
// declaration sets, and assigns those sets to device and user enrollments.
//
// # Design
//
// Each Blueprint is a blueprint.Spec containing an Identifier, optional Name and
// Description, Declarations and Activations. Declarations supply native Apple
// payloads or references to uploaded configuration profiles. Activations name the
// configurations to enable through StandardConfigurations and an optional
// Predicate. The compiler in mdmprotocol/ddm/blueprint resolves these source
// identifiers into device declaration identifiers. One Blueprint owns one DDM
// set, whose name is derived by blueprint.SetName.
//
// Manager.Validate resolves configuration profile references and invokes the
// compiler with the supplied support.Target without writing state. A reference
// to an uploaded profile requires Config.ConfigurationProfiles and a configured
// public HTTPS URL. The manager obtains the upload's URL, size, content type,
// digest and signature status from server/configurationprofile; that package
// owns the file bytes and device download checks.
//
// Manager.Publish compiles the specification and calls Engine.PublishSetTx to
// replace the set's complete contents. Existing enrollment assignments survive;
// omitted declarations leave the set. Publishing a specification containing only
// its Identifier clears the set while retaining assignments. The saved Record
// contains the normalized Spec, Revision, CreatedAt, UpdatedAt and Compiled result,
// including declaration identifier mappings. Records use the state.Store key
// blueprint/spec/<Identifier>; Manager.Get and Manager.List read those records.
//
// Creation requires an empty expected revision. Updates require the current
// Record.Revision; a mismatch returns ddm.ErrConflict. Publishing unchanged
// normalized source retains its revision, and unchanged declaration content
// generates no additional DDM changes. The source revision is separate from
// Apple's declaration ServerToken.
//
// Manager.Assign adds or removes the Blueprint's set assignment for one complete
// mdm.EnrollmentID, including its channel and parent where applicable. It records
// an assignment change only when membership changes. Manager.Delete requires the
// current revision, publishes an empty set to record removals, then deletes the
// set, its assignments and the source record. Stored declarations, their retained
// versions and uploaded configuration profiles remain available in their stores.
//
// Config.Engine and Config.State are required. The DDM transaction must implement
// ddm.PublicationLocker. Mutations call LockPublication for the Blueprint's set
// before locking its source record. For SQL stores, Config.Run must join both
// stores to the same sqlcommon.UnitOfWork so source, declaration membership and
// change records commit or roll back together. Config.Run may be nil with the
// reference memory stores, where the DDM transaction encloses
// the state transaction and no fallible operation follows the state commit.
// Encryption at rest is provided by the configured storage implementations.
//
// Administrative HTTP routes, ETag and If-Match headers, authorization and audit
// handling live in server/internal/app; CLI commands live in server/internal/dmctl.
// The manager records DDM changes, which server/ddmsync turns into
// DeclarativeManagement commands and push notifications. A successful Publish
// confirms server persistence; device synchronization and activation happen
// afterwards through the DDM engine.
//
// # References
//
//   - Implementation: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/blueprints/manager.go
//   - Transaction and revision tests: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/blueprints/manager_test.go
//   - Decision record 0055: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0055-blueprint-composition.md
//   - Decision record 0020: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0022: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0022-change-notifier.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Operations: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/blueprints.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
package blueprints
