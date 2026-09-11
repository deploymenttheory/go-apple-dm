package storage

import (
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
)

// Capability is a three-state observation. Unknown never grants eligibility.
type Capability uint8

// Capability values distinguish unknown from explicitly false.
const (
	CapabilityUnknown Capability = iota
	CapabilityFalse
	CapabilityTrue
)

// Capabilities records device-reported management properties, distinct from ownership admission.
type Capabilities struct {
	Supervised, DEP, UserApproved Capability
	// AppleSilicon comes from an authenticated DeviceInformation response.
	AppleSilicon Capability
	Source       string
	ObservedAt   time.Time
}

func observed(p *bool) Capability {
	if p == nil {
		return CapabilityUnknown
	}
	if *p {
		return CapabilityTrue
	}
	return CapabilityFalse
}

// CapabilitiesFromResult extracts evidence only from an acknowledged, tracked
// device-channel command. requestType must come from the stored queue entry.
func CapabilitiesFromResult(
	old Capabilities,
	id mdm.EnrollmentID,
	requestType string,
	r *mdm.Response,
	at time.Time,
) Capabilities {
	if id.Channel.IsUser() || r == nil || r.Status != mdm.StatusAcknowledged ||
		(requestType != "DeviceInformation" && requestType != "SecurityInfo") {
		return old
	}
	decoded, err := mdm.DecodeResponse(r.Raw, requestType)
	if err != nil {
		return old
	}
	changed := false
	set := func(dst *Capability, v *bool) {
		if v != nil {
			*dst = observed(v)
			changed = true
		}
	}
	switch p := decoded.Payload.(type) {
	case *commands.DeviceInformationResponse:
		set(&old.Supervised, p.QueryResponses.IsSupervised)
		set(&old.AppleSilicon, p.QueryResponses.IsAppleSilicon)
	case *commands.SecurityInfoResponse:
		if m := p.SecurityInfo.ManagementStatus; m != nil {
			set(&old.DEP, m.EnrolledViaDEP)
			set(&old.UserApproved, m.UserApprovedEnrollment)
		}
	}
	if changed {
		old.Source = "device:" + requestType
		old.ObservedAt = at.UTC()
	}
	return old
}
