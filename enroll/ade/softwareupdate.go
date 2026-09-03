package ade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/gdmf"
	"github.com/deploymenttheory/go-apple-dm/plist"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/schema/errors"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

// ErrGate is returned when a gate decision cannot be produced.
var ErrGate = errors.New("ade: software update gate")

// Response content types for the 403 bodies.
const (
	ContentTypeJSON  = "application/json"
	ContentTypePlist = "application/xml"
)

// Target is the version a policy requires. An empty OSVersion means "the
// latest Apple publishes for this device", resolved through gdmf.
type Target struct {
	OSVersion    string
	BuildVersion string
	// RequireBetaProgram enrols the device in a beta program so a seeding
	// version can be enforced.
	RequireBetaProgram *BetaProgram
}

// BetaProgram is the seeding program a Target may require.
type BetaProgram struct {
	Description string
	Token       string
}

// Policy decides the minimum OS for a device. It is given the whole
// MachineInfo (PRODUCT, SOFTWARE_UPDATE_DEVICE_ID, OS_VERSION,
// MANDATORY_SOFTWARE_UPDATE_REQUIRED, ...) and answers whether an update
// is required and to what. It is only consulted when the device says
// MDM_CAN_REQUEST_SOFTWARE_UPDATE.
type Policy interface {
	MinimumOS(ctx context.Context, p *Parsed) (target Target, required bool, err error)
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(ctx context.Context, p *Parsed) (Target, bool, error)

// MinimumOS implements Policy.
func (f PolicyFunc) MinimumOS(ctx context.Context, p *Parsed) (Target, bool, error) { return f(ctx, p) }

// PSSOPolicy is implemented by a Policy that can require Platform SSO
// before enrollment. It is consulted only when the device says
// MDM_CAN_REQUEST_PSSO_CONFIG, and before the software update check.
type PSSOPolicy interface {
	PlatformSSO(ctx context.Context, p *Parsed) (details *schemaerrors.CodePlatformSSORequiredDetails, required bool, err error)
}

// Action is what the gate decided.
type Action int

// Actions.
const (
	// Proceed with enrollment.
	Proceed Action = iota
	// SoftwareUpdateRequired answers 403 with the softwareupdate.required body.
	SoftwareUpdateRequired
	// PSSORequired answers 403 with the psso.required body.
	PSSORequired
)

// String names the action.
func (a Action) String() string {
	switch a {
	case Proceed:
		return "proceed"
	case SoftwareUpdateRequired:
		return "software-update-required"
	case PSSORequired:
		return "psso-required"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Decision is the gate's result: the action, the body to send for a
// refusal, and why.
type Decision struct {
	Action         Action
	SoftwareUpdate *schemaerrors.CodeSoftwareUpdateRequired
	PSSO           *schemaerrors.CodePlatformSSORequired
	Reason         string
}

// Gate runs the software update and Platform SSO checks for one device.
// It proceeds when the device cannot take an update request, when the
// policy is nil or not required, when the device already meets the
// target, or when a "latest" target cannot be resolved because lookup is
// nil or fails (logged). Bodies are validated against the schema before
// they are returned.
func Gate(ctx context.Context, p *Parsed, policy Policy, lookup gdmf.Lookup, logger *slog.Logger) (*Decision, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if policy == nil {
		return &Decision{Action: Proceed, Reason: "no policy"}, nil
	}
	if pp, ok := policy.(PSSOPolicy); ok && flag(p.MDMCANREQUESTPSSOCONFIG) {
		details, required, err := pp.PlatformSSO(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("%w: platform sso policy: %w", ErrGate, err)
		}
		if required {
			return pssoDecision(details)
		}
	}
	if !flag(p.MDMCANREQUESTSOFTWAREUPDATE) {
		return &Decision{Action: Proceed, Reason: "device cannot take a software update request"}, nil
	}
	target, required, err := policy.MinimumOS(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("%w: policy: %w", ErrGate, err)
	}
	if !required {
		return &Decision{Action: Proceed, Reason: "policy does not require an update"}, nil
	}
	if target.OSVersion == "" {
		if lookup == nil {
			logger.WarnContext(ctx, "ade: policy asked for the latest OS but no lookup is configured; proceeding", "serial", p.SERIAL)
			return &Decision{Action: Proceed, Reason: "no lookup for latest"}, nil
		}
		asset, err := lookup.Latest(ctx, deviceID(p))
		if err != nil {
			logger.WarnContext(ctx, "ade: software lookup failed; proceeding", "serial", p.SERIAL, "device", deviceID(p), "error", err)
			return &Decision{Action: Proceed, Reason: "lookup failed: " + err.Error()}, nil
		}
		target.OSVersion, target.BuildVersion = asset.ProductVersion, asset.Build
	}
	if p.OSVERSION != "" && gdmf.CompareVersions(p.OSVERSION, target.OSVersion) >= 0 {
		return &Decision{Action: Proceed, Reason: "device at or above " + target.OSVersion}, nil
	}
	return updateDecision(target)
}

func flag(b *bool) bool { return b != nil && *b }

func deviceID(p *Parsed) string {
	if p.SOFTWAREUPDATEDEVICEID != nil && *p.SOFTWAREUPDATEDEVICEID != "" {
		return *p.SOFTWAREUPDATEDEVICEID
	}
	return p.PRODUCT
}

func updateDecision(t Target) (*Decision, error) {
	body := &schemaerrors.CodeSoftwareUpdateRequired{
		Code:    schemaerrors.ErrorCodeCodeSoftwareUpdateRequired,
		Details: schemaerrors.CodeSoftwareUpdateRequiredDetails{OSVersion: t.OSVersion},
	}
	if t.BuildVersion != "" {
		body.Details.BuildVersion = new(t.BuildVersion)
	}
	if t.RequireBetaProgram != nil {
		body.Details.RequireBetaProgram = &schemaerrors.CodeSoftwareUpdateRequiredDetailsRequireBetaProgram{
			Description: t.RequireBetaProgram.Description, Token: t.RequireBetaProgram.Token,
		}
	}
	if err := body.Validate(support.Target{}); err != nil {
		return nil, fmt.Errorf("%w: softwareupdate.required body: %w", ErrGate, err)
	}
	return &Decision{Action: SoftwareUpdateRequired, SoftwareUpdate: body, Reason: "update to " + t.OSVersion}, nil
}

func pssoDecision(details *schemaerrors.CodePlatformSSORequiredDetails) (*Decision, error) {
	if details == nil {
		return nil, fmt.Errorf("%w: psso.required without details", ErrGate)
	}
	body := &schemaerrors.CodePlatformSSORequired{Code: schemaerrors.ErrorCodeCodePlatformSSORequired, Details: *details}
	if err := body.Validate(support.Target{}); err != nil {
		return nil, fmt.Errorf("%w: psso.required body: %w", ErrGate, err)
	}
	return &Decision{Action: PSSORequired, PSSO: body, Reason: "platform sso required"}, nil
}

// Write sends the decision's 403 body as JSON or plist by the request's
// Accept header. It is an error to write a Proceed decision.
func (d *Decision) Write(w http.ResponseWriter, r *http.Request) error {
	switch d.Action {
	case SoftwareUpdateRequired:
		return WriteError(w, r, http.StatusForbidden, d.SoftwareUpdate)
	case PSSORequired:
		return WriteError(w, r, http.StatusForbidden, d.PSSO)
	}
	return fmt.Errorf("%w: nothing to write for %s", ErrGate, d.Action)
}

// WriteError sends an error body as JSON, or as a plist when the request
// prefers XML, with the matching Content-Type and sniffing disabled.
func WriteError(w http.ResponseWriter, r *http.Request, status int, body any) error {
	var (
		out []byte
		err error
		ct  = ContentTypeJSON
	)
	if wantsPlist(r.Header.Get("Accept")) {
		ct = ContentTypePlist
		out, err = plist.Marshal(body)
	} else {
		out, err = json.Marshal(body)
	}
	if err != nil {
		return fmt.Errorf("%w: encode: %w", ErrGate, err)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(out) // #nosec G705 -- machine-readable JSON or plist with an explicit non-HTML content type
	return nil
}

// wantsPlist reports whether the first acceptable media type is XML or a
// plist rather than JSON. No preference means JSON.
func wantsPlist(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mt, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		mt = strings.ToLower(mt)
		switch {
		case mt == "", mt == "*/*":
			continue
		case strings.Contains(mt, "json"):
			return false
		case strings.Contains(mt, "xml"), strings.Contains(mt, "plist"):
			return true
		}
	}
	return false
}
