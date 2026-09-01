# 0011: Secrets provider and redaction

Status: accepted
Date: 2026-09-01
Phase: 3

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices> (push certificate and key handling)

## References read

- `micromdm/nanomdm@main` `cmd/nanomdm/main.go` (API key and push certificate paths from flags; key bytes flow through plain `[]byte`)
- `fleetdm/fleet` `server/config/config.go` (secrets from environment, `MaskedString` type)
- `micromdm/nanodep@main` `storage` (DEP OAuth tokens stored in plain JSON in the file backend)

## Known pitfalls found

- Plain `[]byte` credentials end up in `%v` logs of config structs.
- File-based secrets mounted by Kubernetes carry a trailing newline that breaks HMAC keys and passwords.
- Path-traversal through secret names when a name is joined onto a directory.

## What they do

- **Fleet**: `MaskedString` redacts in `String()`; sources are environment and YAML.
- **NanoMDM**: flags and files, no redaction type.

## What we do better

1. `secrets.Secret` redacts through `String`, `GoString`, `Format`, `MarshalJSON`, and `MarshalText`, so `fmt`, `slog`, and `encoding/json` all print `[redacted]`; the value is only reachable through `Bytes()`.
2. Providers: `Static`, `Env` (prefixed, name normalised), `Dir` (`os.Root`-scoped so names cannot escape the directory; trailing newline trimmed; size-bounded), and `Chain`.

## Verified by

1. `secrets.TestSecretsRedacted`.
2. `secrets.TestProviders`.

## Rejected alternatives

- Vault or cloud KMS clients in the library: deployments implement `Provider`.
- Encrypting secrets at rest inside storage backends in this phase: deferred to the SQL backends work, which will use the provider for the key.
