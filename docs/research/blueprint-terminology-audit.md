# Blueprint terminology audit

Date: 2026-09-18

Scope: the current branch, `feat/macos27-compatibility-osversion`, and its
uncommitted changes. At review, `origin/main..HEAD` and
`git diff origin/main...HEAD` were empty. The audit therefore covered the
uncommitted Blueprint implementation, including its new packages and changes
to existing packages. Other branches and pull requests were outside scope.

## Corrections

| Previous name or placement | Result | Basis |
|---|---|---|
| Profile storage and delivery in `server/blueprints` | Independent `server/configurationprofile` package | The file is a configuration profile; a Blueprint can reference it. Full package spelling selected by the user. |
| `BlueprintProfile`, `MDM::BlueprintProfile` | `configurationprofile.Info`, `MDM::ConfigurationProfile` | No Apple object called Blueprint Profile was identified. |
| `/blueprint-profiles`, `/v1/blueprint-profile` | `/configuration-profiles`, `/v1/configuration-profile` | Reference-server routes named for configuration profiles. |
| `manageBlueprintProfiles`, `dmctl blueprints profiles` | `manageConfigurationProfiles`, `dmctl configuration-profiles` | Profile administration is independent of Blueprint administration. |
| `blueprint/profile-info/`, `blueprint/profile-data/` | `configuration-profile/info/`, `configuration-profile/data/` | Storage uses the same domain name as the package and API. |
| `Components`, `Component`, `NewComponent` | `Declarations`, `Declaration`, `NewDeclaration` | These inputs represent configurations, assets and management declarations. “Component” came from Jamf's authoring model. |
| Declaration and activation `Key` | `Identifier` | Uses Apple's declaration field name. Source identifiers are resolved to generated device identifiers. |
| `Activation.Configurations` | `Activation.StandardConfigurations` | Matches the ActivationSimple payload field. |
| `HostedProfile`, `ProfileReference`, `ProfileDescriptor`, `Options.Profiles` | `ConfigurationProfile`, `ConfigurationProfileReference`, `ConfigurationProfileDescriptor`, `Options.ConfigurationProfiles` | Identifies the uploaded file accurately; the emitted DDM configuration is LegacyProfile. |
| `UseAsset` | `UseProfileAssetReference` | Explicitly selects Apple's ProfileAssetReference delivery field. This boolean remains a convenience option. |
| Uploaded profile metadata `Identifier`, `UUID` | `PayloadIdentifier`, `PayloadUUID` | Preserves the names of the configuration profile envelope fields. |
| “component keys” and “activation blocks” in feature documentation | Source identifiers, declarations and activations | Uses the concepts represented by the resulting DDM declarations. |

## Package review

| Package | Responsibility and naming result |
|---|---|
| `devicemanagement/mdmprotocol/ddm/blueprint` | Retained. Pure local Blueprint authoring and compilation; its package documentation distinguishes it from Apple-hosted Blueprint resources and the DDM wire format. |
| `server/blueprints` | Retained. Blueprint persistence, revision checks, assignment and publication. Resolves configuration profile references through `server/configurationprofile`. |
| `server/configurationprofile` | Introduced for configuration profile storage and delivery. It has no dependency on either Blueprint package. Ordinary LegacyProfile declarations can use it. |
| `devicemanagement/mdmprotocol/ddm` | Retained. Atomic publication operates on existing server declaration sets. |
| `devicemanagement/storage/ddm/{ddmtest,inmem}`, `server/ddmstore/sqlstore` | Retained. Store contracts, test support and storage implementations; no new Apple protocol concepts. |
| `server/ddmadapter/{internal/proxywire,proxyclient,proxyserver}` | Retained. Existing transport packages now refer to configuration profiles without importing the Blueprint manager. |
| `server/adminauth`, `server/internal/{app,dmctl,dmctl/adminclient}`, `server/service`, `server/e2e` | Retained. Existing implementation packages; new routes, actions, help, tests and references use the corrected names. |

## Convenience format and implementation names

The user explicitly chose to retain convenience objects with corrected
terminology. `Spec`, `ConfigurationProfileReference`, and the flattened
`Activation` input remain local authoring structures. They are not advertised
as Apple-defined objects. The compiler emits Apple's declaration envelope and
payload fields. Generated schema types and wire keys were not renamed.

`Blueprint` remains the local authoring feature name. Apple also uses Blueprint
for an Apple Business resource, but the local format does not claim to implement
that service's request schema. The existing AXM client owns those requests.
[Apple Business: Create a Blueprint](https://developer.apple.com/documentation/applebusinessapi/create-a-blueprint)
documents that separate API.

`PublishSet`, `SetPublication`, `PublishResult`, `PublicationLocker`, source
`Revision`, database lock shards, and storage snapshots describe implementation
operations. They do not denote additional Apple declaration types. Publication
commits server state, while device application remains asynchronous. The initial
schema retains `ddm_publication_locks` in each dialect's `0001_init.sql`.

## Apple references

- [Declarative management data model](https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices): declarations and their four categories.
- [LegacyProfile](https://developer.apple.com/documentation/devicemanagement/legacyprofile): configuration for a legacy profile, using ProfileURL or ProfileAssetReference.
- [ActivationSimple](https://developer.apple.com/documentation/devicemanagement/activationsimple): StandardConfigurations and Predicate.
- [Apple configuration profile payload keys](../../third_party/device-management/mdm/profiles/CommonPayloadKeys.yaml): PayloadIdentifier and PayloadUUID.
- [Vendored Legacy Profile schema](../../third_party/device-management/declarative/declarations/configurations/legacy.yaml) and [data asset schema](../../third_party/device-management/declarative/declarations/assets/data.yaml): declaration types and exact payload field spellings.

The updated examples and API guide are in [Blueprints](../operations/blueprints.md).

## Verification

- Full library and reference-server tests: `go test ./... ./server/...` passed.
- Targeted race tests for configuration profile delivery, Blueprint persistence,
  encryption rotation, the private hop, HTTP routes and CLI commands passed.
- `TestE2E_BlueprintPublication` passed with the race detector.
- Lint reported zero issues in the affected library and server packages.
- The terminology scan found no superseded names in the implementation or its
  usage documentation. Historical names remain in the comparison table above.
- MySQL and PostgreSQL runtime integration was not exercised; no test database
  DSNs were configured. Memory and SQLite behavior was exercised.
