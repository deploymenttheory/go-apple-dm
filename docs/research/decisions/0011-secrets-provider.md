# 0011: Secrets provider and redaction

## Context

Credentials need explicit access and controlled formatting so ordinary logging does not expose them.

## Decision

`secrets.Secret` redacts formatting, text and JSON serialization. Callers retrieve the value explicitly through `Bytes`. Providers support static values, normalized environment names, a bounded directory rooted with `os.Root`, and fallback chains. Directory values have trailing newlines trimmed.

## Rationale

A common value type reduces accidental disclosure in formatted configuration and errors. The provider interface separates secret retrieval from protocol and storage code.

## Constraints

Redaction does not encrypt memory or protect values after `Bytes` is called. Cloud secret managers and KMS integrations belong behind `Provider`; storage sealing is described in record 0013.

## Verification

Secret tests exercise formatting and serialization redaction, provider fallback, missing values, directory bounds and invalid names.

## References

- [secrets](../../../secrets)
- [storage/crypt](../../../storage/crypt)
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `cmd/nanomdm/main.go`, `[]byte`
- `fleetdm/fleet`, `server/config/config.go`, `MaskedString`
- `micromdm/nanodep@main`, `storage`
