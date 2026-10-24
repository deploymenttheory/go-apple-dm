package correlation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// ErrInvalid indicates malformed evidence or an ambiguous mapping.
var ErrInvalid = errors.New("correlation: invalid argument")

// Method is the authenticated enrollment method recorded by the host.
type Method string

const (
	AutomatedDeviceEnrollment     Method = "automated-device-enrollment"
	AccountDrivenDeviceEnrollment Method = "account-driven-device-enrollment"
	ProfileBasedDeviceEnrollment  Method = "profile-based-device-enrollment"
	UserEnrollment                Method = "user-enrollment"
)

// AppleDevice is a successful collection resource from a caller-selected account.
// Resource must be an orgDevices resource; its attributes.serialNumber is the
// reported serial. Fields for resolution are the unmodified resource attributes.
type AppleDevice struct {
	AccountID  string
	Resource   collection.Resource
	ObservedAt time.Time
}

// NativeReport is one successful native device report. Source retains the
// enrollment/channel identity and report kind; Serial is the authenticated device
// serial, not an identifier inferred from an enrollment ID. Fields are the exact
// caller-selected protocol fields. Raw retains the complete original report.
// Omitted fields do not erase earlier reports. Failure attempts belong separately
// in the host's outcome store and must not be supplied as successful evidence.
type NativeReport struct {
	Source        source.Reference
	Serial        string
	Method        Method
	Authenticated bool
	ObservedAt    time.Time
	Fields        map[string]json.RawMessage
	Raw           json.RawMessage
}

// Rejection retains a report that cannot enrich this organization device.
// Reason is missing_serial, serial_conflict, unauthenticated or ineligible_method.
type Rejection struct {
	Report NativeReport
	Reason string
}

// Correlation retains independent originals and only the eligible serial matches.
// Its private evidence prevents resolution over unchecked reports. Inputs and
// returned values are copied; an immutable Correlation supports concurrent reads.
type Correlation struct {
	apple      AppleDevice
	attributes map[string]json.RawMessage
	matched    []NativeReport
	rejected   []Rejection
}

// Correlate explicitly correlates reports with one actual Apple organization
// resource. Serial matching trims surrounding whitespace and ignores case; original
// values remain unchanged. Missing serials and mismatches never match. If one
// source reports different nonempty serials, every report of that source is
// rejected as an identity conflict, including earlier matching reports.
func Correlate(apple AppleDevice, reports []NativeReport) (Correlation, error) {
	var c Correlation
	var resource struct {
		ID         string                     `json:"id"`
		Type       string                     `json:"type"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if apple.AccountID == "" || apple.ObservedAt.IsZero() || json.Unmarshal(apple.Resource.Raw, &resource) != nil || resource.ID == "" || resource.ID != apple.Resource.ID || resource.Type != "orgDevices" || resource.Type != apple.Resource.Type || resource.Attributes == nil {
		return c, fmt.Errorf("%w: actual organization device required", ErrInvalid)
	}
	var serial string
	if raw, ok := resource.Attributes["serialNumber"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &serial) != nil {
			return c, fmt.Errorf("%w: Apple serial", ErrInvalid)
		}
	}
	c.apple = apple
	c.apple.Resource.Raw = clone(apple.Resource.Raw)
	c.attributes = resource.Attributes
	serial = canonical(serial)
	serials := map[source.Reference]string{}
	conflicts := map[source.Reference]bool{}
	for _, report := range reports {
		if report.Source == c.AppleReference() || report.Source.Kind == "" || report.Source.ResourceID == "" || report.ObservedAt.IsZero() || !json.Valid(report.Raw) {
			return Correlation{}, fmt.Errorf("%w: native source, observation time and raw JSON required", ErrInvalid)
		}
		for _, value := range report.Fields {
			if !json.Valid(value) {
				return Correlation{}, fmt.Errorf("%w: native field JSON", ErrInvalid)
			}
		}
		if s := canonical(report.Serial); s != "" && report.Authenticated && eligible(report.Method) {
			if old := serials[report.Source]; old != "" && old != s {
				conflicts[report.Source] = true
			}
			serials[report.Source] = s
		}
	}
	for _, report := range reports {
		report = copyReport(report)
		reason := ""
		switch {
		case !report.Authenticated:
			reason = "unauthenticated"
		case !eligible(report.Method):
			reason = "ineligible_method"
		case conflicts[report.Source]:
			reason = "serial_conflict"
		case serial == "" || canonical(report.Serial) == "":
			reason = "missing_serial"
		case canonical(report.Serial) != serial:
			reason = "serial_conflict"
		}
		if reason == "" {
			c.matched = append(c.matched, report)
		} else {
			c.rejected = append(c.rejected, Rejection{report, reason})
		}
	}
	return c, nil
}

// Apple returns a copy of the original organization-device evidence.
func (c Correlation) Apple() AppleDevice {
	a := c.apple
	a.Resource.Raw = clone(a.Resource.Raw)
	return a
}

// Matched returns copies of the eligible reports with matching authenticated serials.
func (c Correlation) Matched() []NativeReport {
	out := make([]NativeReport, len(c.matched))
	for i, r := range c.matched {
		out[i] = copyReport(r)
	}
	return out
}

// Rejected returns copies of unmatched reports and their exclusion reasons.
func (c Correlation) Rejected() []Rejection {
	out := make([]Rejection, len(c.rejected))
	for i, r := range c.rejected {
		out[i] = Rejection{copyReport(r.Report), r.Reason}
	}
	return out
}

// AppleReference returns the account-scoped opaque source reference for mapping.
func (c Correlation) AppleReference() source.Reference {
	return source.Reference{Kind: "axm.device", AccountID: c.apple.AccountID, ResourceID: c.apple.Resource.ID}
}

// canonical normalizes only the serial matching key, preserving original evidence.
func canonical(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// clone copies retained JSON without decoding or losing numeric precision.
func clone(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }

// copyReport isolates every mutable evidence value from callers.
func copyReport(r NativeReport) NativeReport {
	r.Raw = clone(r.Raw)
	fields := make(map[string]json.RawMessage, len(r.Fields))
	for k, v := range r.Fields {
		fields[k] = clone(v)
	}
	r.Fields = fields
	return r
}

// eligible restricts physical enrichment to the three Device Enrollment methods.
func eligible(method Method) bool {
	return method == AutomatedDeviceEnrollment || method == AccountDrivenDeviceEnrollment || method == ProfileBasedDeviceEnrollment
}
