# Bench diagram review options

Five Archify architecture examples for choosing a reference style. These are review
candidates; the [current bench diagram](../../reference-server-bench.html) is unchanged.
They are separate from the catalogue of 34 maintained diagrams.

Open an interactive preview below. Features can be combined after review; selecting an
option does not require adopting every choice in it.

| Preview | What changes | Best use |
| --- | --- | --- |
| [01. Ownership zones](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/docs/bench-diagram-options/docs/diagrams/review/bench-gold/01-ownership-zones.html) | Three numbered zones: bench control, shared runtime, and validation. | Keep the existing detailed map and give readers a clear entry point. |
| [02. Execution modes](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/docs/bench-diagram-options/docs/diagrams/review/bench-gold/02-execution-modes.html) | Six labelled zones separate simulated checks, live checks, the runtime, scenario selection and evidence. | Compare synthetic fixtures with real credentials without losing the detailed component map. |
| [03. Evidence boundaries](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/docs/bench-diagram-options/docs/diagrams/review/bench-gold/03-evidence-boundaries.html) | Independent contract suites lead to their own Go test output; scenario evidence stays separate. | Make the limits and origins of validation evidence visible in the graph itself. |
| [04. Guided walkthrough](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/docs/bench-diagram-options/docs/diagrams/review/bench-gold/04-guided-walkthrough.html) | Five selectable chapters focus preparation, simulation, process acceptance, live evidence and evidence scope. | Help a new reader explore the detailed map a section at a time. |
| [05. Compact overview](https://htmlpreview.github.io/?https://raw.githubusercontent.com/deploymenttheory/go-apple-dm/docs/bench-diagram-options/docs/diagrams/review/bench-gold/05-compact-overview.html) | Eight primary components replace eleven, with grouped CLI/workspace, launch adapters and runtime/application. | Prioritize an overview while keeping implementation details available through source links. |

## What to compare

- **Zones:** choose the broad groups in option 1, the execution-mode groups in option 2,
  or the explicit test-layer separation in option 3. Dashed regions group related
  concepts; they do not claim network or security isolation.
- **Navigation:** in option 4, try the five guided-view buttons above the legend. These
  select related components; the chapters do not prescribe a single execution sequence.
- **Detail:** option 5 combines components into an overview. Its cards identify what each
  combined component represents. Options 1, 2 and 4 retain eleven components; option 3
  adds a separate contract-output component.
- **Evidence:** click the `SRC` badge on a component for implementation links pinned to
  the reviewed code revision. The CLI and fixture implementations now have explicit
  links. The real device/app is external to this repository.
- **Reader tools:** try the used-colour legend, search, component focus, Light/Dark,
  presentation mode and SVG/PNG export. These are existing Archify capabilities, so
  the examples retain the repository's common colours and controls.

For a detailed reference diagram, combine option 2's mode zones, option 3's independent
contract results, and option 4's guided views. Option 5 is an alternative when a compact
introduction matters more than showing every runtime component separately.

The generic validation-evidence node in options 1, 2, 4 and 5 groups evidence conceptually;
it is not a claim that contract tests write into bench reports. Option 3 makes that
distinction explicit through separate output nodes. Simulated results do not establish
interoperability with a physical Apple device; live delivery evidence must distinguish
API acceptance from device/app receipt.

## Source and validation

All examples link to the same reviewed implementation revision,
`69d3e810d136377ec2e7b21e1da64563e3feb351`, and retain the relevant Go and Apple documentation
links in their public-documentation cards. Each HTML was generated with the repository's
[Archify wrapper and reading policy](../../../../scripts/diagrams/README.md).

- Ownership zones: [HTML source](01-ownership-zones.html) · [diagram specification](src/01-ownership-zones.architecture.json)
- Execution modes: [HTML source](02-execution-modes.html) · [diagram specification](src/02-execution-modes.architecture.json)
- Evidence boundaries: [HTML source](03-evidence-boundaries.html) · [diagram specification](src/03-evidence-boundaries.architecture.json)
- Guided walkthrough: [HTML source](04-guided-walkthrough.html) · [diagram specification](src/04-guided-walkthrough.architecture.json)
- Compact overview: [HTML source](05-compact-overview.html) · [diagram specification](src/05-compact-overview.architecture.json)

The [validation receipt](validation.json) records source/HTML hashes, all nine showcase
checks, source-link checks, browser measurements and interaction/export results.
The upstream browser check reports vertical overflow because these diagrams use the
repository's intentional scrolling document layout. That raw failure is preserved;
separate checks cover horizontal containment and unclipped SVG text at four desktop
sizes in both themes. Perceptual review covers rendered zones, labels, routes and cards.
