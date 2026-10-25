package app

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
)

// ADEMinimumOS executes supplied literal versions. Empty fields are inactive.
// Apple supports enrollment update enforcement on iOS/iPadOS 17+, macOS 14+,
// and visionOS 26+; tvOS has no supported minimum enrollment version gate.
// https://support.apple.com/guide/deployment/depd30715cbb/web
// https://developer.apple.com/documentation/devicemanagement/deploying-software-updates-using-declarative-management
type ADEMinimumOS struct{ IOS, IPadOS, MacOS, VisionOS string }

func (m ADEMinimumOS) active() bool { return m != (ADEMinimumOS{}) }
func (m ADEMinimumOS) validate() error {
	for name, v := range map[string]string{"iOS": m.IOS, "iPadOS": m.IPadOS, "macOS": m.MacOS, "visionOS": m.VisionOS} {
		if v == "" {
			continue
		}
		parsed, err := osversion.Parse(v)
		if err != nil || parsed.IsZero() || !literalOSVersion(v) {
			return fmt.Errorf("%w: invalid ADE %s minimum %q", ErrConfig, name, v)
		}
	}
	return nil
}

// MinimumOS uses successful signed device evidence, never ABM purchasing data.
// A configured requirement cannot be satisfied by an unknown OS version or by
// a device that needs an update but cannot execute Apple's enrollment response.
func (m ADEMinimumOS) MinimumOS(_ context.Context, p *ade.Parsed) (ade.Target, bool, error) {
	var required string
	switch p.Platform {
	case ade.PlatformIPhone, ade.PlatformIPod:
		required = m.IOS
	case ade.PlatformIPad:
		required = m.IPadOS
	case ade.PlatformMac:
		required = m.MacOS
	case ade.PlatformRealityDevice:
		required = m.VisionOS
	case ade.PlatformUnknown:
		if m.active() {
			return ade.Target{}, false, fmt.Errorf("%w: cannot select configured minimum for unknown PRODUCT", ade.ErrGate)
		}
	}
	if required == "" {
		return ade.Target{}, false, nil
	}
	if err := m.validate(); err != nil {
		return ade.Target{}, false, err
	}
	return ade.Target{OSVersion: required}, true, nil
}

// literalOSVersion excludes whitespace, signed numbers and policy expressions.
func literalOSVersion(v string) bool {
	for _, c := range v {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	return true
}
