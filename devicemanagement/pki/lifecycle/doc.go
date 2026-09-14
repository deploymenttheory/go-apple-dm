// Package lifecycle manages certificate setup, renewal and issuer replacement
// using persistent state.
//
// A Manager tracks active and pending revisions separately, so preparing a
// replacement does not interrupt the identity already in use. It validates
// imported certificates against pending keys and publishes activation in the
// same transaction as the workflow change. Supported identities include Apple
// vendor and push certificates, HTTPS certificates and local issuing CAs.
// Public ACME issuance uses HTTP-01 challenges for server HTTPS certificates.
//
// Applications supply storage, trust policy, authorization and scheduling.
// Constructing a Manager starts no background work. Repository records contain
// private keys and must be encrypted by persistent storage adapters. Public
// identity views and activity history omit that key material.
//
// See the certificate lifecycle guide for the reference server's integration:
// https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/certificate-lifecycle.md
package lifecycle
