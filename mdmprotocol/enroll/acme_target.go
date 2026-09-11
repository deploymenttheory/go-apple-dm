package enroll

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

// MacHardware describes known Mac key-generation capabilities. Unknown does
// not infer hardware from a product name. It leaves explicit ACME flags to the
// caller; a known target version is still checked.
type MacHardware string

const (
	MacHardwareUnknown MacHardware = ""
	MacAppleSilicon    MacHardware = "apple-silicon"
	MacT2              MacHardware = "t2"
	MacIntel           MacHardware = "intel"
)

// ValidateTarget checks ACME key combinations and Apple's platform rules.
// macOS 13.1–13.x requires software keys and explicit Attest=false. macOS 14+
// supports hardware keys on Apple silicon and T2, but T2 cannot attest them.
// Availability of the profile or DDM credential itself is checked by its schema.
// Source: https://developer.apple.com/documentation/devicemanagement/acmecertificate
func (a *ACME) ValidateTarget(target support.Target, hardware MacHardware) error {
	if err := a.validate(); err != nil {
		return err
	}
	switch hardware {
	case MacHardwareUnknown, MacAppleSilicon, MacT2, MacIntel:
	default:
		return fmt.Errorf("%w: unknown Mac hardware capability", ErrProfile)
	}
	if target.OS != support.MacOS {
		return nil
	}
	if !target.Version.IsZero() && target.Version.Major < 14 && (a.HardwareBound || a.Attest) {
		return fmt.Errorf(
			"%w: macOS before 14 requires HardwareBound=false and Attest=false",
			ErrProfile,
		)
	}
	if hardware == MacIntel && a.HardwareBound {
		return fmt.Errorf("%w: hardware-bound Mac keys require Apple silicon or T2", ErrProfile)
	}
	if (hardware == MacT2 || hardware == MacIntel) && a.Attest {
		return fmt.Errorf("%w: Mac attestation requires Apple silicon", ErrProfile)
	}
	return nil
}

func defaultFalse(value *bool) *bool {
	if value != nil {
		return value
	}
	return new(false)
}

func macKeyOption(target support.Target, minimum support.Version) bool {
	return target.OS == support.MacOS &&
		(target.Version.IsZero() || target.Version.Compare(minimum) >= 0)
}

func (a *ACME) payloadForTarget(target support.Target) *profiles.ACMECertificate {
	out := a.payload()
	if macKeyOption(target, support.V(13, 1, 0)) {
		out.KeyIsExtractable = defaultFalse(out.KeyIsExtractable)
		out.AllowAllAppsAccess = defaultFalse(out.AllowAllAppsAccess)
	}
	return out
}

func (s *SCEP) payloadForTarget(target support.Target) *profiles.SCEP {
	out := s.payload()
	if target.OS != support.MacOS || macKeyOption(target, support.V(10, 13, 4)) {
		out.PayloadContent.KeyIsExtractable = defaultFalse(out.PayloadContent.KeyIsExtractable)
	}
	if macKeyOption(target, support.V(10, 10, 0)) {
		out.PayloadContent.AllowAllAppsAccess = defaultFalse(out.PayloadContent.AllowAllAppsAccess)
	}
	return out
}

func macDefaultFalse(value *bool, target support.Target, minimum support.Version) *bool {
	if macKeyOption(target, minimum) {
		return defaultFalse(value)
	}
	return value
}
