# Diagram colour legend

Every diagram uses the same eight purpose colours and three outcome colours. Its legend
shows the colours used in that diagram. Light and dark themes use different shades with the
same meanings; SVG and image exports preserve them.

| Colour | Meaning | Examples |
|---|---|---|
| Blue | Services and processing | MDM service, SCEP server, command dispatch, pending work |
| Cyan | Communication and transport | HTTP endpoints, API clients, protocol exchanges, notifications |
| Teal | Storage and persistence | Enrollment records, command queues, snapshots, certificate stores |
| Indigo | Authentication and identity verification | Client identity, CMS/JWS verification, bearer tokens, challenge validation |
| Violet | Certificates and cryptography | Certificate authorities, issuance, signing, encryption, key management |
| Magenta | Authorization and policy | Admission, issuance policy, Cedar evaluation, permission checks |
| Brown / bronze | Configuration and artifacts | Enrollment profiles, declarations, generated schemas, configuration files |
| Slate | Actors, structure, and neutral states | Devices, administrators, external providers, boundaries, reset and checkout |
| Green | Successful or accepted | Acknowledged commands, enabled enrollment, successful certificate response |
| Orange / amber | Warning, deferred progress, or retry | NotNow, rate limiting, retry conditions |
| Red | Failed, rejected, or invalid | Rejected issuance, failed commands, invalid-token events, error responses |

## Applying the legend

- Colour the responsibility being explained. A certificate **store** is teal; an identity
  **issuer** is violet; an issuance **policy** is magenta. An API endpoint is cyan even when
  its requests require authentication; colour the authentication check indigo.
- Describe actions separately from their results. Credential validation is indigo;
  rejection is red; an explicit successful result is green. A service or normal processing
  path does not become green simply because it appears in the successful scenario.
- Mixed results keep their purpose colour. “Accepted or rejected”, “HTTP 200 or 404”, and
  an APNs response carrying a status and reason are not uniformly successful or failed.
  APNs acceptance also does not establish device or application receipt.
- Waiting for ordinary progress is blue or neutral. A NotNow response or rate limit is
  orange. Clearing a command, resetting enrollment, and checking out are neutral actions,
  rather than failures.
- A boundary is a slate structural outline. Being inside a security boundary does not
  constitute an error, warning, or authorization result.
- Line width and dashes describe emphasis and interaction style independently of colour.
  A response can remain dashed whether it carries success, failure, or ordinary data.
- Labels and role symbols remain essential. Never rely on colour alone to explain a
  distinction, especially between neighbouring blue, indigo, and violet shades.

In [SCEP issuance](flow-scep-issuance.html), the SCEP server is blue, issuance policy is
magenta, the identity CA is violet, and challenge-password validation is indigo. The signed
failure CertRep is red; the successful CertRep is green.

## Authoring

The JSON sources opt in with `meta.colour_profile: "purpose-v1"`. Every node and relationship
has a stable `id` and an explicit `purpose`; explanatory cards use the corresponding value
in `dot`. Allowed values are `service`, `transport`, `storage`, `authentication`, `certificate`,
`policy`, `artifact`, `neutral`, `success`, `warning`, and `failure`.

Component `type` still describes the component or lifecycle state. It no longer determines
colour. Set `meta.legend.mode` to `hidden` to replace the upstream type legend with the
shared purpose legend. The repository renderer generates that legend before validation and
delivery, including it in canonical exports.

Use the [repository reading profile](../../scripts/diagrams/README.md) for regeneration.
The palette and its paired theme shades live in
[`scripts/diagrams/colours.mjs`](../../scripts/diagrams/colours.mjs).
