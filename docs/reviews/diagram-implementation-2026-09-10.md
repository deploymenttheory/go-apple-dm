# Diagram review implementation — 10 September 2026

The [catalogue](../diagrams/README.md) contains 31 revised, standalone HTML diagrams:
the original 27 and four companions. The implementation uses the
[code and terminology review](diagram-review-2026-09-10.md) against
`ff85958196cb013e8e14c1055f067319b07e3299`, including PRs 12 and 14.
The [machine-readable receipt](diagram-implementation-2026-09-10.json) binds every source,
HTML artifact, browser measurement, and README image to its SHA-256 hash.

## Outputs

- [Controlled enrollment-profile replacement](../diagrams/flow-enrollment-profile-replacement.html)
  covers preparation, separate wake, old-identity delivery, candidate check-in, commit conditions,
  cancellation, failure, and expiry.
- [APNs certificate workflows](../diagrams/apns-certificate-workflows.html) separates MDM
  customer credentials from app notification credentials and explains connection retirement.
- [App notification delivery](../diagrams/flow-app-notification-delivery.html) distinguishes
  APNs acceptance from app receipt and the bench's optional correlation evidence.
- [Reference-server bench](../diagrams/reference-server-bench.html) connects supervision,
  embedded/process execution, the scenario catalogue, fixture boundaries, and live evidence.

The original diagrams now explain responsibilities and outcomes alongside protocol identifiers.
Notable corrections include independent check-in dispatch, complete declarative responses,
conditional enrollment branches, certificate acquisition, command-result persistence, and
ordinary reset versus controlled replacement. Storage descriptions distinguish composed and
optional interfaces, conditional SQL encryption, and the separate app-push state namespace.
Administration, runtime, shutdown, authentication, and test-evidence descriptions reflect the
reviewed code. Source badges and guided-view notes were also checked for stale wording.

Every diagram retains explanatory cards and includes clickable public documentation. The set
uses 31 distinct public URLs, with Apple documentation for protocol behavior and Go, Cedar, or
RFC references where they better explain the implementation. The review records the supporting
code paths; architecture sources additionally carry Git-verified component references.

## Reading layout

The user chose to retain detail with larger text and vertical scrolling after reviewing the
[earlier previews](diagram-implementation-2026-09-10.previews.md). The final layout widens the
reading area, increases node and relationship text, and uses 15px explanatory text. The
toolbar has its own row so it cannot overlap titles or guided views at laptop sizes. Long
diagrams and reference cards remain in normal document flow. There is no internal diagram
scroller or clipping used to force a one-screen fit.

The [repository reading profile](../../scripts/diagrams/README.md) stages an isolated Archify
2.17 copy before validation and delivery. It leaves the installed skill untouched and keeps
all nine showcase checks enabled. Lifecycle/data-flow spacing and clickable card references
are part of this profile, so regeneration must use the repository wrapper.

## Shared colour semantics

All 31 sources and HTML artifacts now use the agreed [shared colour legend](../diagrams/colours.md):
blue for services, cyan for transport, teal for storage, indigo for authentication, violet for
certificates and cryptography, magenta for authorization and policy, bronze for configuration
and artifacts, and slate for actors, structure, and neutral states. Green, orange, and red
are reserved for success, warning or deferral, and failure.

Each node and relationship has an explicit purpose independent of its component type or line
variant. Card dots follow the same vocabulary. The renderer generates a legend containing the
colours used by that diagram, including matching arrowhead colours in both themes and exports.
Neutral boundary outlines no longer imply a warning or security failure.

The SCEP issuance policy is magenta, its identity CA violet, and challenge validation indigo.
Only the rejected CertRep is red; the successful CertRep is green. In the command lifecycle,
NotNow is orange, Acknowledged is green, and Cleared is neutral. Checkout and enrollment reset
are also neutral. Mixed outcomes, including APNs responses that may accept or reject a send,
retain a purpose colour rather than implying success.

The user selected option 5 from the [five-option ACME review](../diagrams/acme-legend-options.html):
an interactive key above each diagram, headed **Legend**, with **All Components** to restore
the full view. Only colours used in that diagram appear. Selecting a colour highlights its
components and connections, with source-derived counts, a short meaning, and named examples.
Connection-only outcomes remain selectable. The selected state is exposed through labelled
buttons and a live description; arrow keys, Home/End, Enter/Space, and Escape support keyboard use.

Native node focus, guided views, and component-type filters take over cleanly from colour
highlighting. Printing and canonical exports retain the complete SVG and a static key, even
when a category is selected. The static key also serves embedded and JavaScript-disabled views.
The comparison page retains all five layouts as review evidence, with the selected labels updated.

The key retains the detailed labels and user-selected vertical scrolling. The component and
relationship SVG content is unchanged in all 31 diagrams; the diagram JSON sources are also
unchanged by this legend update. Lifecycle role symbols for NotNow and Cleared continue to
identify waiting and neutral states rather than errors.

## Verification

- **Artifact validation:** all 31 deliveries passed 9/9 showcase checks with zero errors and
  warnings. Final source and HTML hashes match their delivery receipts.
- **Browser measurements:** 186 observations cover all 31 diagrams at four light-theme desktop
  sizes, plus dark mode at 1440×900 and 2048×1320. No horizontal overflow, SVG text outside the
  canvas, clipped controls, or page-load JavaScript errors were detected. All public-reference
  links match the authored URLs.
- **Colour checks:** all 186 observations confirm node, arrow, and arrowhead colours and
  legends matching the purposes used. All 31 light/dark renders were visually reviewed.
  Twelve integration checks also confirm that the colour extension accepts valid sources while
  rejecting missing purposes, invalid values, missing relationship IDs, unknown core fields,
  and invalid relationships. They also cover connection-only legend entries, safe label
  serialization, and isolation between diagrams with and without the colour profile.
- **Upstream browser status:** `failed` for intentional vertical scrolling. Readability,
  viewer-control placement, and capture checks passed. This raw status is retained separately
  from the supplemental assessment under the user-selected scrolling policy.
- **Visual review:** light/dark full-page renders were inspected for every diagram, with
  additional inspection of dense diagrams at 1440px. Explanatory cards and references were
  included in that review.
- **Interaction checks:** theme switching, node search, focus/clear, and every available guided
  view passed across the set. Seven representatives cover all five diagram types plus SCEP
  and ACME, with SVG and PNG exports in both themes: 28 checks while a colour is highlighted.
  Exported SVG node and edge colours were measured in both explicit themes; PNG pixels were
  checked for each used palette colour. SVG exports contained no active viewer controls,
  focus, or colour-filter state and retained
  the complete static legend. Every used colour was selected and restored in both themes
  across all 31 diagrams, including source-derived counts and native exploration handoffs.
  ACME additionally passed real Enter/Space activation, presentation layout, a 390px-wide
  legend layout, embedded fallback, and JavaScript-disabled fallback checks.
- **README assets:** both architecture images were regenerated through the viewer's canonical
  full-diagram PNG export, excluding viewer controls. The root README now links to 31 diagrams.

Browser screenshot/contact-sheet sidecars are generated locally and ignored by Git. Their
paths and hashes are recorded in the receipt. They can be regenerated with the catalogue's
instructions; automated screenshots do not themselves establish visual approval.

The focused Go tests passed during the preceding review, as recorded there. This implementation
changes documentation and diagram tooling; it does not establish a new coverage-gate result,
SQL backend acceptance result, or live Apple-device validation result.

## Representation choices

The check-in map replaces its old workflow source with an architecture source under the same
HTML basename. Each branch now dispatches by MessageType; it does not imply that every device
sends all nine messages in order.

Declarative operations have separate response nodes so each branch reaches an outcome.
Internal certificate binding, registration, and promotion work is explained in notes/cards
rather than represented as an external protocol exchange. Sequence labels identify optional
or mutually exclusive paths; they do not prescribe a mandatory ordering for all devices.

The overview shows selected control paths and explains shared persistence in its cards.
Detailed identity, storage, and worker behavior remains in the corresponding diagrams.

The test-harness picture intentionally differs from one proposed review arrow: built-process
acceptance feeds process evidence, not an unconditional coverage-profile edge. The current
`bench-build` / `test-acceptance` targets do not instrument the built server for coverage.
The coverage gate merges emitted profiles, while live-device evidence remains separate.
This follows the [Makefile](../../Makefile) and
[coverage script](../../scripts/coverage-gate.sh).
