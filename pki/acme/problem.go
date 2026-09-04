package acme

import (
	"errors"
	"fmt"
	"net/http"
)

// ProblemPrefix is the URN namespace RFC 8555 section 6.7 gives ACME
// errors.
const ProblemPrefix = "urn:ietf:params:acme:error:"

// Problem types this server answers with. The set is the part of RFC 8555
// section 6.7 that a device-attest-01 server can actually reach, plus
// badAttestationStatement from the ACME device attestation draft.
const (
	// ProblemAccountDoesNotExist is a kid naming an account we do not have.
	ProblemAccountDoesNotExist = "accountDoesNotExist"
	// ProblemBadCSR is a certificate request the server will not sign.
	ProblemBadCSR = "badCSR"
	// ProblemBadNonce is a stale, unknown, or reused nonce. A client is
	// expected to retry with the fresh nonce on the response.
	ProblemBadNonce = "badNonce"
	// ProblemBadPublicKey is a key the server will not certify.
	ProblemBadPublicKey = "badPublicKey"
	// ProblemBadSignatureAlgorithm is a JWS alg outside the supported set;
	// the response carries the set.
	ProblemBadSignatureAlgorithm = "badSignatureAlgorithm"
	// ProblemBadAttestationStatement is an attestation that does not verify.
	ProblemBadAttestationStatement = "badAttestationStatement"
	// ProblemMalformed is a request that does not parse or does not obey
	// the protocol.
	ProblemMalformed = "malformed"
	// ProblemOrderNotReady is a finalize before the authorization is valid.
	ProblemOrderNotReady = "orderNotReady"
	// ProblemRejectedIdentifier is an identifier the server will not issue
	// for: unknown, already used, or naming a device the policy refuses.
	ProblemRejectedIdentifier = "rejectedIdentifier"
	// ProblemUnauthorized is a request the account may not make.
	ProblemUnauthorized = "unauthorized"
	// ProblemUnsupportedIdentifier is an identifier type we do not issue
	// for.
	ProblemUnsupportedIdentifier = "unsupportedIdentifier"
	// ProblemServerInternal is our fault.
	ProblemServerInternal = "serverInternal"
)

// Problem is an RFC 7807 problem document as RFC 8555 profiles it. It is
// both the wire form and an error, so a handler can return one and the
// server can render it.
type Problem struct {
	Type        string        `json:"type"`
	Detail      string        `json:"detail,omitempty"`
	Status      int           `json:"status,omitempty"`
	Subproblems []*Subproblem `json:"subproblems,omitempty"`
	// Algorithms is the accepted set, sent with badSignatureAlgorithm as
	// RFC 8555 section 6.2 requires.
	Algorithms []string `json:"algorithms,omitempty"`
	// wrapped carries the cause for the server log. It never reaches the
	// device: a client learns the shape of the fault, not our internals.
	wrapped error
}

// Subproblem names the identifier a problem is about.
type Subproblem struct {
	Type       string      `json:"type"`
	Detail     string      `json:"detail,omitempty"`
	Identifier *Identifier `json:"identifier,omitempty"`
}

// Error implements error.
func (p *Problem) Error() string {
	if p.Detail == "" {
		return "acme: " + p.Type
	}
	return "acme: " + p.Type + ": " + p.Detail
}

// Unwrap exposes the cause to errors.Is and errors.As, and to the log.
func (p *Problem) Unwrap() error { return p.wrapped }

// Is lets a caller test for a problem type with errors.Is.
func (p *Problem) Is(target error) bool {
	other, ok := target.(*Problem)
	return ok && other.Type == p.Type && other.Detail == ""
}

// URN is the full type URI sent on the wire.
func (p *Problem) URN() string { return ProblemPrefix + p.Type }

// Terminal reports whether the fault is the client's and repeating the
// request unchanged would fail the same way. A terminal fault settles an
// order or a challenge invalid; a non-terminal one leaves it alone so a
// retry can succeed once the server is well again.
func (p *Problem) Terminal() bool { return p.Status < http.StatusInternalServerError }

// problemStatus maps each type to the status RFC 8555 gives it.
var problemStatus = map[string]int{
	ProblemAccountDoesNotExist:     http.StatusBadRequest,
	ProblemBadCSR:                  http.StatusBadRequest,
	ProblemBadNonce:                http.StatusBadRequest,
	ProblemBadPublicKey:            http.StatusBadRequest,
	ProblemBadSignatureAlgorithm:   http.StatusBadRequest,
	ProblemBadAttestationStatement: http.StatusBadRequest,
	ProblemMalformed:               http.StatusBadRequest,
	ProblemOrderNotReady:           http.StatusForbidden,
	ProblemRejectedIdentifier:      http.StatusBadRequest,
	ProblemUnauthorized:            http.StatusUnauthorized,
	ProblemUnsupportedIdentifier:   http.StatusBadRequest,
	ProblemServerInternal:          http.StatusInternalServerError,
}

// NewProblem builds a problem of the given type.
func NewProblem(kind, format string, args ...any) *Problem {
	status, ok := problemStatus[kind]
	if !ok {
		status = http.StatusBadRequest
	}
	p := &Problem{Type: kind, Status: status}
	if format != "" {
		p.Detail = fmt.Sprintf(format, args...)
	}
	return p
}

// WrapProblem builds a problem that carries a cause for the log while
// showing the device only what the detail says.
func WrapProblem(kind string, err error, format string, args ...any) *Problem {
	p := NewProblem(kind, format, args...)
	p.wrapped = err
	return p
}

// WithSubproblem adds a subproblem naming an identifier.
func (p *Problem) WithSubproblem(kind, detail string, id Identifier) *Problem {
	p.Subproblems = append(p.Subproblems, &Subproblem{
		Type:       ProblemPrefix + kind,
		Detail:     detail,
		Identifier: &id,
	})
	return p
}

// AsProblem converts any error into a problem document. An error that
// already is one is returned unchanged; anything else becomes an internal
// error, so an unexpected fault never leaks its message to a device.
func AsProblem(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return WrapProblem(ProblemServerInternal, err, "the server could not complete the request")
}

// Problem sentinels for errors.Is, so callers can test a kind without
// building a document.
var (
	ErrMalformed        = NewProblem(ProblemMalformed, "")
	ErrBadNonce         = NewProblem(ProblemBadNonce, "")
	ErrUnauthorized     = NewProblem(ProblemUnauthorized, "")
	ErrBadCSR           = NewProblem(ProblemBadCSR, "")
	ErrBadAttestation   = NewProblem(ProblemBadAttestationStatement, "")
	ErrRejected         = NewProblem(ProblemRejectedIdentifier, "")
	ErrOrderNotReady    = NewProblem(ProblemOrderNotReady, "")
	ErrAccountNotFound  = NewProblem(ProblemAccountDoesNotExist, "")
	ErrUnsupportedID    = NewProblem(ProblemUnsupportedIdentifier, "")
	ErrInternal         = NewProblem(ProblemServerInternal, "")
	ErrBadAlgorithm     = NewProblem(ProblemBadSignatureAlgorithm, "")
	ErrBadPublicKeyType = NewProblem(ProblemBadPublicKey, "")
)
