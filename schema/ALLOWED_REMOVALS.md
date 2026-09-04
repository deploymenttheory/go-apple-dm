# Allowed removals

`EXPORTED_IDENTIFIERS.lock` records every exported identifier the generator has
ever emitted under `schema/`. Regeneration adds identifiers but never removes
one on its own, so a schema refresh cannot silently rename or drop a type,
field, constant, or method. `make verify` fails when a locked identifier is no
longer generated.

This file is the permission to let one go. A rename is an addition and a
removal, and only the removal needs authorising: the new identifier appears in
the lock by itself. An outright deletion, where Apple withdraws a command
rather than renaming it, is recorded here in exactly the same way.

To remove an identifier deliberately, add a line below in the form
"- `package/Identifier` reason", then run `make generate`; the identifier
leaves the lock and `make verify` passes again. Keep entries so the history is
readable.

## Entries

<!-- - `commands/Example.OldField` renamed to Example.NewField in schema refresh 2026-09-01 -->
