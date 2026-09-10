# Diagram implementation previews (historical)

This records the earlier pause for layout direction. The user subsequently chose larger text
and vertical scrolling. See the [completed implementation](diagram-implementation-2026-09-10.md)
for current outputs and checks; the linked HTML files now contain the final versions.

These are work-in-progress outputs from the review implementation, paused for layout direction. **10 of the planned 31 diagrams have newly rendered HTML.** All 10 passed Archify’s nine deterministic artifact checks with zero composition errors or warnings. That does not establish browser fit or visual acceptance.

Four representative previews were opened and visually inspected at 1440 × 900 in light mode. All four scroll vertically; the ADE and replacement sequences also overflow horizontally. The node context is still too small in the architecture maps. Full dark-theme, viewport, and interaction verification is pending.

## Start here

| Preview | What changed | Layout issue to decide |
| --- | --- | --- |
| [Check-in dispatch](../diagrams/checkin-dispatch.html) | Independent message handlers replace the false enrollment-to-checkout chain. | Small node context; explanatory cards and references extend below the first screen. |
| [Automated Device Enrollment](../diagrams/flow-ade-enrollment.html) | Direct and web-auth entry conditions, update rejection, identity issuance, and final TokenUpdate acknowledgement. | Long sequence and reference text need scrolling; alternatives still need clearer visual grouping. |
| [Controlled profile replacement](../diagrams/flow-enrollment-profile-replacement.html) | New companion showing preparation, separate wake, candidate check-in, and commit conditions. | Dense timeline; long relationship labels; important ordering caveats are below the diagram. |
| [Storage backends](../diagrams/storage-backends.html) | Replacement state, conditional encryption, and separate app credential storage. | Small diagram text and a second card row. |

## All newly rendered previews

| Diagram | Type | Artifact checks |
| --- | --- | --- |
| [MDM Check-in Messages and Their Effects](../diagrams/checkin-dispatch.html) | architecture | 9/9; zero errors or warnings |
| [Account-driven enrollment](../diagrams/flow-account-driven-enrollment.html) | sequence | 9/9; zero errors or warnings |
| [ACME with Managed Device Attestation](../diagrams/flow-acme-attestation.html) | sequence | 9/9; zero errors or warnings |
| [Automated Device Enrollment](../diagrams/flow-ade-enrollment.html) | sequence | 9/9; zero errors or warnings |
| [MDM Command Delivery](../diagrams/flow-command-delivery.html) | sequence | 9/9; zero errors or warnings |
| [Declarative Device Management: Change, Synchronize, Report](../diagrams/flow-ddm-sync.html) | sequence | 9/9; zero errors or warnings |
| [Controlled Enrollment-profile Replacement](../diagrams/flow-enrollment-profile-replacement.html) | sequence | 9/9; zero errors or warnings |
| [SCEP Identity Issuance](../diagrams/flow-scep-issuance.html) | sequence | 9/9; zero errors or warnings |
| [Storage Backends, Dialects and Sealed Columns](../diagrams/storage-backends.html) | architecture | 9/9; zero errors or warnings |
| [storage.Store: the Eight Interfaces](../diagrams/storage-contract.html) | architecture | 9/9; zero errors or warnings |

The other 21 planned sources are drafts with unresolved layout diagnostics; their existing HTML must not be treated as the revised output. The diagram catalogue, README images, and final acceptance record are still pending. The old check-in workflow source also remains temporarily alongside its replacement architecture source.

The reviewed explanatory copy and public references are retained in the source drafts. No further shortening, splitting, or movement of explanatory content into separate reading notes has been performed since the request to share previews first.

[Preview receipts and browser measurements](diagram-implementation-2026-09-10.preview.json) record the exact source and HTML hashes for these outputs.
