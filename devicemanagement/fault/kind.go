package fault

import "slices"

// Kind classifies a failure for every caller and every transport. A kind carries no
// domain of its own, so one boundary can map every package's failures onto a status
// without knowing which package raised them.
//
// The names follow the canonical error codes gRPC and Google's API design guide share,
// because API clients already understand that vocabulary, but the set is HTTP's: each
// kind is one status a transport answers with, so Conflict, Gone, PayloadTooLarge,
// UnsupportedMediaType and Upstream exist where gRPC has AlreadyExists, Aborted and
// FailedPrecondition, and gRPC's DataLoss and OutOfRange do not. The number is carried
// as a plain integer so this package stays free of net/http and a non-HTTP transport
// can still read it.
//
// A Kind is an error value, so errors.Is(err, fault.NotFound) reports whether a failure
// is of that kind regardless of which condition or package raised it, and KindOf
// returns the value for a switch. Kinds are compared by identity and never constructed
// outside this package.
//
// The set is deliberately small. A condition that needs more detail says so in its
// message and its code, not by adding a kind that only one caller understands.
type Kind struct {
	name   string
	text   string
	status int
}

// Error returns the kind's text, so a bare kind reads naturally as an error.
func (k *Kind) Error() string { return k.text }

// String returns the kind's stable name, in the form used for the error.type
// attribute: lower case with underscores.
func (k *Kind) String() string { return k.name }

// HTTPStatus returns the status code the kind aligns with.
func (k *Kind) HTTPStatus() int { return k.status }

// Server reports whether the kind describes a failure of the deployment rather than of
// the request: a 5xx status. Transports withhold the detail of such failures and a span
// records them as errors, where a request failure is not.
func (k *Kind) Server() bool { return k.status >= 500 }

// Kind returns the kind itself, so a bare kind used as an error classifies through the
// same interface as a catalogued condition.
func (k *Kind) Kind() *Kind { return k }

var (
	// NotFound is an addressed thing that does not exist.
	NotFound = &Kind{"not_found", "not found", 404}
	// InvalidArgument is a request the server understood and rejected.
	InvalidArgument = &Kind{"invalid_argument", "invalid argument", 400}
	// Conflict is a request refused because it disagrees with existing state.
	Conflict = &Kind{"conflict", "conflicting state", 409}
	// Unauthenticated is a missing or unusable credential.
	Unauthenticated = &Kind{"unauthenticated", "unauthenticated", 401}
	// PermissionDenied is an authenticated caller without the required permission, or
	// a device the server refuses to serve.
	PermissionDenied = &Kind{"permission_denied", "permission denied", 403}
	// ResourceExhausted is a rate limit, a quota or a capacity bound that was reached.
	ResourceExhausted = &Kind{"resource_exhausted", "resource exhausted", 429}
	// Unimplemented is an operation this server does not provide.
	Unimplemented = &Kind{"unimplemented", "not implemented", 501}
	// Unavailable is a dependency that is configured but not usable now.
	Unavailable = &Kind{"unavailable", "unavailable", 503}
	// DeadlineExceeded is an operation that ran out of time.
	DeadlineExceeded = &Kind{"deadline_exceeded", "deadline exceeded", 504}
	// Upstream is a dependency that answered, but not usefully: an Apple service or a
	// receiver returned a failure this server relays rather than causes.
	Upstream = &Kind{"upstream", "upstream failure", 502}
	// Gone is a thing that existed and was deliberately removed. Apple's MDM protocol
	// relies on it: a device whose UserAuthenticate is answered with 410 stops managing
	// that user channel.
	Gone = &Kind{"gone", "gone", 410}
	// PayloadTooLarge is a request body over the bound the server accepts. It is an
	// HTTP-shaped kind because the bound is a property of the transport, and a caller
	// reacts to it differently from any other rejected argument: by sending less.
	PayloadTooLarge = &Kind{"payload_too_large", "payload too large", 413}
	// UnsupportedMediaType is a request body in a format the route does not read.
	UnsupportedMediaType = &Kind{"unsupported_media_type", "unsupported media type", 415}
	// Internal is a failure the caller cannot act on and must not see the detail of.
	Internal = &Kind{"internal", "internal error", 500}
	// Cancelled is a request the caller abandoned before it was answered. No HTTP
	// status describes it, so it carries 499, the number nginx logs for a client that
	// went away; it is not a failure of the server, and Server reports false, so it is
	// never counted against the deployment or logged as one of its errors.
	Cancelled = &Kind{"cancelled", "cancelled by the caller", 499}
)

var kinds = []*Kind{
	NotFound, InvalidArgument, Conflict, Unauthenticated, PermissionDenied,
	ResourceExhausted, Unimplemented, Unavailable, DeadlineExceeded, Upstream, Gone,
	PayloadTooLarge, UnsupportedMediaType, Internal, Cancelled,
}

// Kinds returns every kind, in a stable order. The slice is a copy.
func Kinds() []*Kind { return slices.Clone(kinds) }

// KindForUpstreamStatus classifies an answer from an Apple service, or any other
// dependency this server calls on a caller's behalf, by the rule every such client
// shares. A 400, 404 or 409 is the caller's: this server relayed the caller's request
// and the verdict on it, so InvalidArgument, NotFound and Conflict. A 429 is
// ResourceExhausted. A 408 or 504 is DeadlineExceeded. A 401 or 403 is the
// deployment's credential, which the caller cannot correct and a retry will not fix
// until an operator acts, so Unavailable. Anything else is Upstream.
func KindForUpstreamStatus(status int) *Kind {
	switch status {
	case 400:
		return InvalidArgument
	case 401, 403:
		return Unavailable
	case 404:
		return NotFound
	case 408, 504:
		return DeadlineExceeded
	case 409:
		return Conflict
	case 429:
		return ResourceExhausted
	}
	return Upstream
}
