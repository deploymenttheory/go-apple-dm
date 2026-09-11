package app

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

type acmeTarget struct {
	target     support.Target
	hardware   enroll.MacHardware
	credential bool
}

func (s *acmeService) resolveMac(
	ctx context.Context,
	b acme.Binding,
	known enroll.MacHardware,
) (enroll.MacHardware, error) {
	if known != enroll.MacHardwareUnknown || s.cfg.MacHardware == nil {
		return known, nil
	}
	return s.cfg.MacHardware(ctx, b)
}

func (e *enrollment) profileForDevice(
	ctx context.Context,
	b acme.Binding,
	identity, product, version string,
	hardware enroll.MacHardware,
) (*enroll.Profile, error) {
	target := support.Target{OS: support.OSFromProduct(product)}
	if version != "" {
		v, err := support.ParseVersion(version)
		if err != nil {
			return nil, fmt.Errorf("%w: device OS version is invalid", enroll.ErrProfile)
		}
		target.Version = v
	}
	if target.OS == support.MacOS && e.acme != nil {
		var err error
		hardware, err = e.acme.resolveMac(ctx, b, hardware)
		if err != nil {
			return nil, fmt.Errorf("app: Mac capabilities: %w", err)
		}
	}
	return e.profileWithIdentity(ctx, b, identity, acmeTarget{target: target, hardware: hardware})
}

func (t acmeTarget) apply(payload *enroll.ACME) error {
	if t.target.OS == support.MacOS {
		switch {
		case !t.target.Version.IsZero() && t.target.Version.Major < 14,
			t.hardware == enroll.MacIntel,
			t.credential && t.hardware == enroll.MacHardwareUnknown:
			payload.HardwareBound, payload.Attest = false, false
		case t.hardware == enroll.MacT2:
			payload.Attest = false
		}
	}
	if err := payload.ValidateTarget(t.target, t.hardware); err != nil {
		return fmt.Errorf("app: ACME target: %w", err)
	}
	return nil
}
