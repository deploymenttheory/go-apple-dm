package acme

import (
	"context"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/acme/attest"
)

// Decision is everything known when the server is about to accept a
// challenge: what the order asked for, what the server expected of the
// device, and what the device proved. A policy sees all three, because a
// useful rule needs more than one of them.
type Decision struct {
	Account    *Account
	Order      *Order
	Binding    Binding
	Identifier Identifier
	// Attestation is what the device proved, or nil when it sent no
	// attestation. A nil attestation only reaches a policy when the server
	// is configured not to require one.
	Attestation *attest.Attestation
}

// Properties are the attested device facts, or the zero value when there
// was no attestation.
func (d *Decision) Properties() attest.Properties {
	if d.Attestation == nil {
		return attest.Properties{}
	}
	return d.Attestation.Properties
}

// Policy decides whether to issue. It runs after the attestation has been
// verified and after the attested device has been matched against the
// binding, so a policy is asked "should this device get a certificate",
// never "is this attestation genuine".
//
// A policy that refuses should return a problem wrapping ErrRejected or
// ErrUnauthorized, which settles the challenge invalid. Any other error is
// treated as a server fault: the challenge stays pending and the client may
// retry, because a lookup that failed is not a device that was refused.
// step-ca and nanoca ship without this seam, which is why the step-ca
// documentation warns that it trusts any Apple device.
type Policy interface {
	Authorize(ctx context.Context, d *Decision) error
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(ctx context.Context, d *Decision) error

// Authorize implements Policy.
func (f PolicyFunc) Authorize(ctx context.Context, d *Decision) error { return f(ctx, d) }

// AllowAll issues to any device that produced a valid attestation for a
// recognised identifier. It is the default, and it is only as strong as the
// identifiers: a deployment that mints identifiers per device already has
// its allowlist.
func AllowAll() Policy {
	return PolicyFunc(func(context.Context, *Decision) error { return nil })
}

// AllowSerials issues only to the listed serial numbers.
func AllowSerials(serials ...string) Policy {
	allowed := make(map[string]bool, len(serials))
	for _, s := range serials {
		allowed[s] = true
	}
	return PolicyFunc(func(_ context.Context, d *Decision) error {
		serial := d.Properties().SerialNumber
		if serial == "" || !allowed[serial] {
			return NewProblem(
				ProblemUnauthorized,
				"the device is not on the list of devices allowed to enroll",
			).WithSubproblem(
				ProblemRejectedIdentifier, "unknown device", d.Identifier,
			)
		}
		return nil
	})
}

// DeviceLookup issues only to devices a lookup recognises, which is how an
// Apple Business Manager or device enrollment service assignment becomes an
// enrollment condition. A lookup that fails is a server fault, not a
// refusal, so a device is never turned away because a directory was down.
type DeviceLookup func(ctx context.Context, serial string) (found bool, err error)

// Authorize implements Policy.
func (l DeviceLookup) Authorize(ctx context.Context, d *Decision) error {
	serial := d.Properties().SerialNumber
	if serial == "" {
		return NewProblem(
			ProblemUnauthorized,
			"the attestation names no serial number, so ownership cannot be checked",
		)
	}
	found, err := l(ctx, serial)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the device could not be looked up")
	}
	if !found {
		return NewProblem(ProblemUnauthorized, "the device is not assigned to this organisation")
	}
	return nil
}

// RequireSecureBoot refuses a Mac whose secure boot status is not one of
// those listed. A device that reports no status is refused as well, since
// the absence of the extension is not evidence of a secured device.
func RequireSecureBoot(allowed ...string) Policy {
	return PolicyFunc(func(_ context.Context, d *Decision) error {
		got := d.Properties().SecureBoot
		if got == "" || !slices.Contains(allowed, got) {
			return NewProblem(
				ProblemUnauthorized,
				"the device does not meet the secure boot requirement",
			)
		}
		return nil
	})
}

// RequireSIP refuses a Mac with System Integrity Protection disabled, or
// one that does not report it.
func RequireSIP() Policy {
	return PolicyFunc(func(_ context.Context, d *Decision) error {
		enabled := d.Properties().SIPEnabled
		if enabled == nil || !*enabled {
			return NewProblem(
				ProblemUnauthorized,
				"the device does not report System Integrity Protection as enabled",
			)
		}
		return nil
	})
}

// Chain runs policies in order and stops at the first refusal.
func Chain(policies ...Policy) Policy {
	return PolicyFunc(func(ctx context.Context, d *Decision) error {
		for _, p := range policies {
			if err := p.Authorize(ctx, d); err != nil {
				return err
			}
		}
		return nil
	})
}
