package applications

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Metadata describes a package independently of its storage provider. Pointer fields
// distinguish an omitted preference from an explicit false, zero or empty value.
// Update replaces the whole value; it is not a partial merge. Digests are optional
// expectations checked against source bytes, never assertions that verification passed.
// Installer preferences without a native Apple equivalent remain catalogue metadata;
// CheckNativeDelivery rejects requests that would require those preferences to be applied.
type Metadata struct {
	PackageName          string  `json:"packageName"`
	FileName             string  `json:"fileName"`
	CategoryID           *string `json:"categoryId,omitempty"`
	Info                 *string `json:"info,omitempty"`
	Notes                *string `json:"notes,omitempty"`
	Priority             *int    `json:"priority,omitempty"`
	OSRequirements       *string `json:"osRequirements,omitempty"`
	FillUserTemplate     *bool   `json:"fillUserTemplate,omitempty"`
	FillExistingUsers    *bool   `json:"fillExistingUsers,omitempty"`
	SWU                  *bool   `json:"swu,omitempty"`
	RebootRequired       *bool   `json:"rebootRequired,omitempty"`
	SelfHealNotify       *bool   `json:"selfHealNotify,omitempty"`
	SelfHealingAction    *string `json:"selfHealingAction,omitempty"`
	OSInstall            *bool   `json:"osInstall,omitempty"`
	SerialNumber         *string `json:"serialNumber,omitempty"`
	ParentPackageID      *string `json:"parentPackageId,omitempty"`
	BasePath             *string `json:"basePath,omitempty"`
	SuppressUpdates      *bool   `json:"suppressUpdates,omitempty"`
	IgnoreConflicts      *bool   `json:"ignoreConflicts,omitempty"`
	SuppressFromDock     *bool   `json:"suppressFromDock,omitempty"`
	SuppressEula         *bool   `json:"suppressEula,omitempty"`
	SuppressRegistration *bool   `json:"suppressRegistration,omitempty"`
	InstallLanguage      *string `json:"installLanguage,omitempty"`
	OSInstallerVersion   *string `json:"osInstallerVersion,omitempty"`
	Format               *string `json:"format,omitempty"`
	Digests
	// BundleID and Version identify the application for native delivery and inventory.
	// They are optional for a metadata-only record, but required for delivery.
	BundleID string `json:"bundleID,omitempty"`
	Version  string `json:"version,omitempty"`
}

// DecodeMetadata rejects unknown fields, read-only resource properties, extra JSON
// values and oversized input instead of silently discarding caller intent.
func DecodeMetadata(r io.Reader) (Metadata, error) {
	var m Metadata
	b, err := io.ReadAll(io.LimitReader(r, 128<<10+1))
	if err != nil {
		return m, err
	}
	if len(b) > 128<<10 {
		return m, ErrTooLarge
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: metadata: %w", ErrInvalid, err)
	}
	var extra any
	if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
		return m, fmt.Errorf("%w: metadata must be one JSON object", ErrInvalid)
	}
	return m, ValidateMetadata(m)
}

// ValidateMetadata validates all editable fields without contacting storage. OS
// requirements are comma-separated exact versions or trailing .x wildcard patterns.
// Priority is a nonnegative scheduling hint; a lower number precedes a higher one.
func ValidateMetadata(m Metadata) error {
	if strings.TrimSpace(m.PackageName) == "" || !validText(m.PackageName, 255, false) {
		return invalidField("packageName")
	}
	if !validFileName(m.FileName, ".pkg") {
		return invalidField("fileName")
	}
	if !validText(m.BundleID, 255, false) || strings.ContainsAny(m.BundleID, " /\\") {
		return invalidField("bundleID")
	}
	if !validText(m.Version, 64, false) {
		return invalidField("version")
	}
	for _, f := range []struct {
		name      string
		value     *string
		limit     int
		multiline bool
	}{
		{"categoryId", m.CategoryID, 255, false},
		{"info", m.Info, 16384, true},
		{"notes", m.Notes, 16384, true},
		{"osRequirements", m.OSRequirements, 1024, false},
		{"selfHealingAction", m.SelfHealingAction, 128, false},
		{"serialNumber", m.SerialNumber, 1024, false},
		{"parentPackageId", m.ParentPackageID, 64, false},
		{"basePath", m.BasePath, 4096, false},
		{"installLanguage", m.InstallLanguage, 64, false},
		{"osInstallerVersion", m.OSInstallerVersion, 64, false},
		{"format", m.Format, 32, false},
	} {
		if f.value != nil && !validText(*f.value, f.limit, f.multiline) {
			return invalidField(f.name)
		}
	}
	if m.Priority != nil && *m.Priority < 0 {
		return invalidField("priority")
	}
	if m.ParentPackageID != nil && *m.ParentPackageID != "" && !ValidID(*m.ParentPackageID) {
		return invalidField("parentPackageId")
	}
	if m.BasePath != nil && *m.BasePath != "" {
		p := *m.BasePath
		if strings.Contains(p, "\\") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
			return invalidField("basePath")
		}
	}
	if m.Format != nil && *m.Format != "" && *m.Format != "flat-pkg" {
		return invalidField("format")
	}
	if m.InstallLanguage != nil && *m.InstallLanguage != "" && !languagePattern.MatchString(*m.InstallLanguage) {
		return invalidField("installLanguage")
	}
	if _, err := parseRequirements(value(m.OSRequirements)); err != nil {
		return err
	}
	return m.Digests.Validate()
}

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}([-_][A-Za-z0-9]{2,8})*$`)

// invalidField identifies the rejected property while preserving ErrInvalid.
func invalidField(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

// value reads an optional text preference without changing its stored presence.
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// enabled reports an explicitly requested boolean behavior.
func enabled(p *bool) bool { return p != nil && *p }

// validText bounds UTF-8 metadata and rejects unexpected control characters.
func validText(s string, limit int, multiline bool) bool {
	if len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 && !(multiline && (c == '\n' || c == '\r' || c == '\t')) || c == 127 {
			return false
		}
	}
	return true
}

// validFileName checks a display basename without allowing path separators.
func validFileName(s, suffix string) bool {
	return len(s) > len(suffix) && validText(s, 255, false) && !strings.ContainsAny(s, "/\\") && strings.HasSuffix(strings.ToLower(s), suffix) && strings.TrimSpace(s) == s
}

type versionPattern struct {
	parts    []uint64
	wildcard bool
}

// parseVersion parses bounded numeric components and an optional trailing wildcard.
func parseVersion(s string, wildcards bool) (versionPattern, error) {
	var out versionPattern
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, invalidField("osRequirements")
	}
	for i, p := range parts {
		if wildcards && p == "x" && i == len(parts)-1 && i > 0 {
			out.wildcard = true
			break
		}
		if p == "" {
			return out, invalidField("osRequirements")
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return out, invalidField("osRequirements")
			}
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return out, invalidField("osRequirements")
		}
		out.parts = append(out.parts, n)
	}
	return out, nil
}

// parseRequirements validates every comma-separated OS version alternative.
func parseRequirements(s string) ([]versionPattern, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []versionPattern
	for _, part := range strings.Split(s, ",") {
		p, err := parseVersion(strings.TrimSpace(part), true)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// SupportsOS evaluates OSRequirements. Exact versions compare numerically with
// omitted trailing components treated as zero. For example 15.x includes 15.8.1;
// 15.8 is exactly 15.8.0. Empty requirements allow any valid numeric OS version.
func (m Metadata) SupportsOS(version string) (bool, error) {
	actual, err := parseVersion(version, false)
	if err != nil {
		return false, fmt.Errorf("%w: device OS version", ErrInvalid)
	}
	patterns, err := parseRequirements(value(m.OSRequirements))
	if err != nil {
		return false, err
	}
	if len(patterns) == 0 {
		return true, nil
	}
	for _, p := range patterns {
		matches := true
		for i := range 3 {
			if p.wildcard && i >= len(p.parts) {
				break
			}
			var want, got uint64
			if i < len(p.parts) {
				want = p.parts[i]
			}
			if i < len(actual.parts) {
				got = actual.parts[i]
			}
			if want != got {
				matches = false
				break
			}
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

// UnsupportedOptions lists preferences which native MDM and DDM package delivery
// cannot enforce. False/unset booleans and empty/"nothing" self-healing actions
// request no extra behavior. RebootRequired also needs orchestration, so it is not
// silently converted into an unattended restart.
func (m Metadata) UnsupportedOptions() []string {
	out := []string{}
	for _, f := range []struct {
		name string
		p    *bool
	}{
		{"fillUserTemplate", m.FillUserTemplate},
		{"fillExistingUsers", m.FillExistingUsers},
		{"swu", m.SWU},
		{"rebootRequired", m.RebootRequired},
		{"selfHealNotify", m.SelfHealNotify},
		{"osInstall", m.OSInstall},
		{"suppressUpdates", m.SuppressUpdates},
		{"ignoreConflicts", m.IgnoreConflicts},
		{"suppressFromDock", m.SuppressFromDock},
		{"suppressEula", m.SuppressEula},
		{"suppressRegistration", m.SuppressRegistration},
	} {
		if enabled(f.p) {
			out = append(out, f.name)
		}
	}
	for _, f := range []struct{ name, value string }{
		{"serialNumber", value(m.SerialNumber)}, {"basePath", value(m.BasePath)}, {"installLanguage", value(m.InstallLanguage)},
	} {
		if f.value != "" {
			out = append(out, f.name)
		}
	}
	if s := value(m.SelfHealingAction); s != "" && s != "nothing" {
		out = append(out, "selfHealingAction")
	}
	return out
}

// CheckNativeDelivery validates required application identity, OS eligibility and
// unsupported installer options before constructing either native delivery request.
func (m Metadata) CheckNativeDelivery(osVersion string) error {
	if err := ValidateMetadata(m); err != nil {
		return err
	}
	if m.BundleID == "" || m.Version == "" {
		return fmt.Errorf("%w: bundleID and version are required for delivery", ErrInvalid)
	}
	if options := m.UnsupportedOptions(); len(options) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(options, ", "))
	}
	ok, err := m.SupportsOS(osVersion)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: OS requirements", ErrIneligible)
	}
	return nil
}
