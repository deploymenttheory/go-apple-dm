# Renames

`NAMES.lock` records every exported identifier the generator has ever emitted
under `schema/`. Regeneration adds names but never removes one on its own, so
a schema refresh cannot silently rename or drop a type, field, constant, or
method. `make verify` fails when a locked name is no longer generated.

To remove or rename an identifier deliberately, add a line here in the form
"- `package/Identifier` reason", then run `make generate`; the name leaves the
lock and `make verify` passes again. Keep entries so the history is readable.

## Allowed removals

<!-- - `commands/Example.OldField` renamed to Example.NewField in schema refresh 2026-09-01 -->
