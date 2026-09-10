# Diagram reading profile

The documentation diagrams retain detailed explanations with larger text and normal vertical
page scrolling. `archify.mjs` applies this profile to an isolated copy of Archify **2.17**,
then delegates validation and delivery to its CLI. The installed skill and delivered HTML
are never patched in place. All nine showcase artifact checks remain enabled.

The profile changes node and relationship typography together with label measurements,
widens the reading area, puts the toolbar in its own row, spaces lifecycle/data-flow columns,
and renders public-reference
card items as escaped links. It disables the viewer's adaptive height squeeze; it does not
hide content or introduce an internal scrolling canvas. Workflow and architecture geometry
is authored in the source JSON.

The shared [colour legend](../../docs/diagrams/colours.md) uses eight purpose colours, reserving
green, orange, and red for success, warnings or deferral, and failure. `colours.mjs` defines the
paired theme shades, SVG markers, and legend. Sources opt in with
`meta.colour_profile: "purpose-v1"` and give every node and relationship an explicit `purpose`.
Card dots use the same vocabulary. Missing purposes, unknown values, and relationships without
stable IDs fail validation. The installed component types remain available for role symbols;
they do not assign colour. Boundaries are neutral, and line styles are independent of outcome.

The isolated copy extends the schema descriptions and explicitly validates the colour fields.
Archify's precompiled validators then validate a copy containing every original core field,
with only the validated extension removed and purpose card dots represented as a supported
neutral dot. The authored source is unchanged. Unknown core fields and invalid relationships
still fail; the delivered receipt binds the complete source, including its colour assignments.
The purpose legend is added during rendering, before all nine artifact checks and delivery.

The default skill location is `~/.agents/skills/archify`. Set `ARCHIFY_SKILL_DIR` to use another
installation. No npm dependencies are needed. The temporary copy is cached using the profile
and upstream file hashes; `.ready` in that directory records the provenance. `--prepare`
prints its path. Missing patch anchors or an incompatible version fail explicitly.

From the repository root:

```bash
node scripts/diagrams/archify.mjs validate architecture docs/diagrams/src/system-architecture.architecture.json \
  --quality showcase --repo-root . --json
node scripts/diagrams/archify.mjs deliver architecture docs/diagrams/src/system-architecture.architecture.json \
  docs/diagrams/system-architecture.html --quality showcase --repo-root . --json
node scripts/diagrams/archify.mjs visual-check docs/diagrams/system-architecture.html --json
```

`deliver` binds the exact source and HTML hashes. `visual-check` separately measures the
delivered artifact in Chrome. Its upstream one-screen policy flags vertical scrolling as a
failure. Keep that result and record supplemental assessment against this repository's
reading policy: no horizontal overflow, readable unclipped text, clear routes, accessible
cards, and working controls. Do not reduce content or shrink text to suppress intentional
vertical scrolling.

Before upgrading Archify, review every patch anchor and revalidate all diagram types. The
lifecycle and data-flow sources rely on this profile's wider grid. See the
[catalogue](../../docs/diagrams/README.md) for the full set and review evidence.

Run the colour-extension checks with `node --test scripts/diagrams/colours.test.mjs`.
Browser review must also check node borders, arrows and arrowheads, used-colour legends, and
both themes of canonical SVG/PNG exports. A successful export download alone does not prove
that its colours survived serialization.
