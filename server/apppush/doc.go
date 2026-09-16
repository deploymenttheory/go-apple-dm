// Package apppush stores app APNs certificates separately from MDM credentials.
//
// # Design
//
// Store validates a certificate and its private key before replacing the
// credential for a topic. Each replacement increments a persistent version.
// PushCertificate reloads the credential for each send, so server replicas see
// committed renewals without restarting. Administrative listings expose only
// metadata.
//
// Persistent state backends must supply an encryption keyring. Encrypted records
// are bound to their storage keys so they cannot be moved between topics.
//
// # References
//
//   - APNs certificate handling: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/pki/pushcert/doc.go
//   - Encrypted storage: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/storage/crypt/doc.go
package apppush
