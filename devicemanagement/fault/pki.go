package fault

// Certificate identities and their lifecycle: the managed HTTPS, enrollment, push and
// vendor identities an administrator drives through the setup API.
var (
	PKIInvalid = NewClient(
		"DM-PKI-INVALID", InvalidArgument,
		"the certificate request is not acceptable",
	)
	PKIConflict = NewClient(
		"DM-PKI-CONFLICT", Conflict,
		"the transition conflicts with the identity's current state",
	)
	PKICertificateNotFound = NewClient(
		"DM-PKI-CERTIFICATE-NOT-FOUND", NotFound,
		"the certificate does not exist",
	)
)

// Certificate signing and revocation, as driven by an administrator or a device
// presenting a certificate.
var (
	PKICSRInvalid = NewClient(
		"DM-PKI-CSR-INVALID", InvalidArgument,
		"the certificate signing request is not valid",
	)
	PKIPolicyViolation = NewClient(
		"DM-PKI-POLICY-VIOLATION", PermissionDenied,
		"the certificate request violates the issuing policy",
	)
	// PKICertificateRevoked is a Conflict: the administration API raises it when a
	// revocation is requested for a certificate already in that state. A device
	// presenting a revoked certificate is refused by the transport under its own
	// device condition.
	PKICertificateRevoked = NewClient(
		"DM-PKI-CERTIFICATE-REVOKED", Conflict,
		"the certificate has already been revoked",
	)
	PKICertificateExpired = NewClient(
		"DM-PKI-CERTIFICATE-EXPIRED", PermissionDenied,
		"the certificate is outside its validity period",
	)
	PKIRevocationInvalid = NewClient(
		"DM-PKI-REVOCATION-INVALID", InvalidArgument,
		"the certificate, reason or issuer is not acceptable",
	)
	PKIIssuerUnknown = NewClient(
		"DM-PKI-ISSUER-UNKNOWN", NotFound,
		"the issuer is not known to this server",
	)
	// ACMENotFound is the one ACME store condition an API caller meets, when
	// inspecting an account or order that does not exist. A record the store will not
	// hold, or one that lost a race, is the deployment's concern and is declared by the
	// acme package as an operator condition.
	ACMENotFound = NewClient(
		"DM-ACME-NOT-FOUND", NotFound,
		"the ACME record does not exist",
	)
)

// Push certificates an administrator uploads. The pushcert package depends on nothing
// in this module, so the transport that accepts an upload wraps its errors with these.
var (
	PushCertInvalid = NewClient(
		"DM-PUSHCERT-INVALID", InvalidArgument,
		"the push certificate or its key is not valid",
	)
	PushCertTopicMissing = NewClient(
		"DM-PUSHCERT-TOPIC-MISSING", InvalidArgument,
		"the push certificate carries no APNs topic",
	)
	PushCertKeyMismatch = NewClient(
		"DM-PUSHCERT-KEY-MISMATCH", InvalidArgument,
		"the private key does not match the push certificate",
	)
)
