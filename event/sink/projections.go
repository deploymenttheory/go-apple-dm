package sink

import (
	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
)

// Default is the projection table for every event type this module declares.
//
// Registering a type with nil says its metadata is the whole story. Leaving a
// type out entirely is not a way to say that: TestEveryEventTypeIsProjected
// fails, because the difference between "considered and bare" and "forgotten"
// is the difference this table exists to keep.
func Default() *Registry {
	r := NewRegistry()

	// Check-in. Authenticate carries the device's own description of itself,
	// which is what makes an enrollment record worth auditing.
	r.Register(event.Enrolled, authenticate)
	r.Register(event.Reenrolled, authenticate)

	// TokenUpdate is the reason this package is default-deny. The message
	// carries UnlockToken, the secret that clears a device passcode, plus the
	// push token, PushMagic, and the user's short and long names. NanoMDM and
	// MicroMDM both forward the whole check-in body base64-encoded, so all of
	// that reaches the webhook receiver. Three booleans and a topic are what
	// an audit trail actually needs.
	r.Register(event.TokenUpdated, tokenUpdate)

	r.Register(event.CheckedOut, nil)
	r.Register(event.BootstrapTokenSet, nil)
	r.Register(event.EnrollmentImported, nil)
	r.Register(event.CertRotated, str("cert_hash"))
	r.Register(event.CertReuseDenied, enrollmentIDs)
	r.Register(event.UserAuthenticated, str("user_id"))
	r.Register(event.UserAuthFailed, str("user_id"))

	// Commands. The command plist and the device's response body are both
	// left behind; the identifiers and the outcome are the audit record.
	r.Register(event.CommandQueued, command)
	r.Register(event.CommandSent, command)
	r.Register(event.CommandResult, response)

	r.Register(event.PushTokenInvalid, pushResult)

	r.Register(event.DDMChanged, ddmChanges)
	r.Register(event.DDMStatusReceived, nil)

	r.Register(event.ACMEChallengeValid, passthrough("identifier", "serial"))
	r.Register(event.ACMEIssued, passthrough("serial", "identifier", "device"))
	r.Register(event.AttestationRejected, passthrough("identifier", "reason"))

	r.Register(event.AdminAction, passthrough("Action", "Method", "Path", "TokenID"))
	r.Register(event.AdminDenied, passthrough("Action", "Method", "Path", "TokenID", "Reason"))

	// The DEP vocabulary lives in package dep by design, so its projections
	// live here beside the rest rather than forcing dep to import a sink.
	r.Register(dep.EventDeviceAdded, depDevice)
	r.Register(dep.EventDeviceModified, depDevice)
	r.Register(dep.EventDeviceDeleted, depDevice)
	r.Register(dep.EventDeviceAssigned, depAssignment)
	r.Register(dep.EventTokenExpiring, depTokenExpiring)

	return r
}

func authenticate(data any) map[string]any {
	m, ok := data.(*checkin.Authenticate)
	if !ok {
		return nil
	}
	out := map[string]any{}
	putPtr(out, "serial_number", m.SerialNumber)
	putNonEmpty(out, "model", m.Model)
	putPtr(out, "product_name", m.ProductName)
	putPtr(out, "os_version", m.OSVersion)
	putPtr(out, "build_version", m.BuildVersion)
	putNonEmpty(out, "topic", m.Topic)
	return out
}

// tokenUpdate deliberately reads three fields and no more. Adding one here is
// a decision to publish it; the sentinel test in this package is what stops
// that decision being made by accident.
func tokenUpdate(data any) map[string]any {
	m, ok := data.(*checkin.TokenUpdate)
	if !ok {
		return nil
	}
	out := map[string]any{"not_on_console": m.NotOnConsole}
	putNonEmpty(out, "topic", m.Topic)
	if m.AwaitingConfiguration != nil {
		out["awaiting_configuration"] = *m.AwaitingConfiguration
	}
	return out
}

func command(data any) map[string]any {
	c, ok := data.(*mdm.Command)
	if !ok {
		return nil
	}
	out := map[string]any{}
	putNonEmpty(out, "command_uuid", c.UUID)
	putNonEmpty(out, "request_type", c.RequestType)
	return out
}

// response keeps the error codes and domains, which say what failed, and
// leaves the localised descriptions behind: they are unbounded free text
// chosen by the device, and the codes are what an operator matches on.
func response(data any) map[string]any {
	resp, ok := data.(*mdm.Response)
	if !ok {
		return nil
	}
	out := map[string]any{"status": string(resp.Status)}
	putNonEmpty(out, "command_uuid", resp.CommandUUID)
	if len(resp.ErrorChain) > 0 {
		errs := make([]map[string]any, 0, len(resp.ErrorChain))
		for _, e := range resp.ErrorChain {
			item := map[string]any{"error_code": e.ErrorCode}
			putNonEmpty(item, "error_domain", e.ErrorDomain)
			errs = append(errs, item)
		}
		out["errors"] = errs
	}
	return out
}

func pushResult(data any) map[string]any {
	r, ok := data.(push.Result)
	if !ok {
		return nil
	}
	out := map[string]any{"status": r.Status, "invalid": r.Invalid}
	putNonEmpty(out, "reason", r.Reason)
	return out
}

func ddmChanges(data any) map[string]any {
	rows, ok := data.([]ddm.Change)
	if !ok {
		return nil
	}
	reasons := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, c := range rows {
		if c.Reason != "" && !seen[c.Reason] {
			seen[c.Reason] = true
			reasons = append(reasons, c.Reason)
		}
	}
	out := map[string]any{"changes": len(rows)}
	if len(reasons) > 0 {
		out["reasons"] = reasons
	}
	return out
}

func depDevice(data any) map[string]any {
	e, ok := data.(dep.DeviceEvent)
	if !ok {
		return nil
	}
	out := map[string]any{}
	putNonEmpty(out, "account", e.Account)
	putNonEmpty(out, "serial_number", e.Device.SerialNumber)
	putNonEmpty(out, "phase", string(e.Phase))
	return out
}

func depAssignment(data any) map[string]any {
	e, ok := data.(dep.AssignmentEvent)
	if !ok {
		return nil
	}
	out := map[string]any{}
	putNonEmpty(out, "account", e.Account)
	putNonEmpty(out, "serial_number", e.Assignment.SerialNumber)
	putNonEmpty(out, "profile_uuid", e.Assignment.ProfileUUID)
	return out
}

func depTokenExpiring(data any) map[string]any {
	e, ok := data.(dep.TokenExpiringEvent)
	if !ok {
		return nil
	}
	out := map[string]any{"expiry": e.Expiry}
	putNonEmpty(out, "account", e.Account)
	return out
}

func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// putPtr is putNonEmpty for the optional keys the generated check-in types
// model as pointers.
func putPtr(m map[string]any, key string, val *string) {
	if val != nil {
		putNonEmpty(m, key, *val)
	}
}
