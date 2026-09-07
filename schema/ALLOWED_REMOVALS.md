# Allowed removals

`EXPORTED_IDENTIFIERS.lock` records the exported identifiers guarded by the schema
generator. Regeneration adds identifiers automatically. `make verify` fails when
a locked type, field, constant or method is no longer generated and its removal
has not been recorded here.

To authorize an intentional removal, add an entry below in the form
"- `package/Identifier` reason", then run `make generate`. The identifier leaves
the lock, and verification accepts its absence. A rename requires an entry for
the removed name; the new name is added to the lock automatically. Retain entries
to document API compatibility changes.

## Entries

<!-- - `commands/Example.OldField` renamed to Example.NewField -->
