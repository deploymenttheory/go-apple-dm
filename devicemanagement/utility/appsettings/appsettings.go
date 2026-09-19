package appsettings

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
)

var (
	ErrInput    = errors.New("appsettings: invalid input")
	ErrIdentity = errors.New("appsettings: required verified identity is unavailable")
)

// MatchMode selects the identifiers emitted into a binary rule.
type MatchMode string

const (
	MatchCDHash    MatchMode = "cdhash"
	MatchApp       MatchMode = "app"
	MatchTeam      MatchMode = "team"
	MatchSigningID MatchMode = "signing-id"
)

// BinaryOptions describes policy, not discovered facts. PathPrefix and
// SigningState narrow every emitted rule and are never inferred from a path or
// certificate. AlwaysAllowManagedApps is applicable only to allow rules.
type BinaryOptions struct {
	Match                  MatchMode `json:"match"`
	PathPrefix             string    `json:"pathPrefix,omitempty"`
	SigningState           string    `json:"signingState,omitempty"`
	AlwaysAllowManagedApps *bool     `json:"alwaysAllowManagedApps,omitempty"`
}

// PrivacyEntry identifies one app and its explicitly selected permissions.
// Permissions is Apple's generated record, including OrganizationJustification.
type PrivacyEntry struct {
	BundleID              string                       `json:"bundleID"`
	DesignatedRequirement string                       `json:"designatedRequirement,omitempty"`
	Permissions           ddm.AppSettingsAppDictionary `json:"permissions"`
}

// AllowApps constructs a nonempty app allow list. Empty lists are rejected
// because the generated payload serialization omits empty arrays.
func AllowApps(ids []string, target support.Target) (*ddm.AppSettings, error) {
	return apps(ids, target, true)
}

// DenyApps constructs a nonempty app deny list, preserving the caller's order.
func DenyApps(ids []string, target support.Target) (*ddm.AppSettings, error) {
	return apps(ids, target, false)
}

func apps(ids []string, target support.Target, allow bool) (*ddm.AppSettings, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: at least one bundle ID required", ErrInput)
	}
	for _, id := range ids {
		if !identifier(id) {
			return nil, fmt.Errorf("%w: invalid bundle ID", ErrInput)
		}
	}
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
	copyIDs := append([]string(nil), ids...)
	if allow {
		payload.Allowed.AllowedApps = copyIDs
	} else {
		payload.Allowed.DeniedApps = copyIDs
	}
	return validate(payload, target)
}

// AllowBinaries constructs rules for every inspected architecture. Exact hashes
// differ by architecture; identical rules from other match modes are deduplicated.
func AllowBinaries(id appidentity.Identity, options BinaryOptions, target support.Target) (*ddm.AppSettings, error) {
	return binaries(id, options, target, true)
}

// DenyBinaries constructs denial rules, including the signing-ID-only mode.
func DenyBinaries(id appidentity.Identity, options BinaryOptions, target support.Target) (*ddm.AppSettings, error) {
	return binaries(id, options, target, false)
}

type rule struct{ hash, signing, team, prefix, state string }

func binaries(id appidentity.Identity, o BinaryOptions, target support.Target, allow bool) (*ddm.AppSettings, error) {
	switch o.Match {
	case MatchCDHash, MatchApp, MatchTeam:
	case MatchSigningID:
		if allow {
			return nil, fmt.Errorf("%w: signing-id alone cannot allow a binary", ErrInput)
		}
	default:
		return nil, fmt.Errorf("%w: explicit match mode required", ErrInput)
	}
	if !allow && o.AlwaysAllowManagedApps != nil {
		return nil, fmt.Errorf("%w: managed-app exception requires allow rules", ErrInput)
	}
	if o.PathPrefix != "" && (!path.IsAbs(o.PathPrefix) || hasControl(o.PathPrefix)) {
		return nil, fmt.Errorf("%w: absolute path prefix required", ErrInput)
	}
	if len(id.Architectures) == 0 {
		return nil, ErrIdentity
	}
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
	if o.AlwaysAllowManagedApps != nil {
		payload.Allowed.AlwaysAllowManagedApps = new(*o.AlwaysAllowManagedApps)
	}
	seen, names := make(map[rule]bool), make(map[string]bool)
	for _, a := range id.Architectures {
		if a.Name == "" || names[a.Name] || a.Signature.Status != appidentity.Valid {
			return nil, fmt.Errorf("%w: missing, duplicate, or unverified architecture %q", ErrIdentity, a.Name)
		}
		names[a.Name] = true
		r, err := identityRule(a, o)
		if err != nil {
			return nil, err
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		if allow {
			payload.Allowed.AllowedBinaries = append(payload.Allowed.AllowedBinaries, ddm.AppSettingsAllowedAllowedBinaries{
				CDHash: optional(r.hash), SigningID: optional(r.signing), TeamID: optional(r.team), PathPrefix: optional(r.prefix), SigningState: optional(r.state),
			})
		} else {
			payload.Allowed.DeniedBinaries = append(payload.Allowed.DeniedBinaries, ddm.AppSettingsAllowedDeniedBinaries{
				CDHash: optional(r.hash), SigningID: optional(r.signing), TeamID: optional(r.team), PathPrefix: optional(r.prefix), SigningState: optional(r.state),
			})
		}
	}
	return validate(payload, target)
}

func identityRule(a appidentity.Architecture, o BinaryOptions) (rule, error) {
	r := rule{prefix: o.PathPrefix, state: o.SigningState}
	switch o.Match {
	case MatchCDHash:
		h, err := hex.DecodeString(a.CDHash)
		if err != nil || len(h) != 20 {
			return rule{}, fmt.Errorf("%w: 40-character CDHash required", ErrIdentity)
		}
		r.hash = strings.ToLower(a.CDHash)
	case MatchApp, MatchSigningID:
		if !identifier(a.SigningID) {
			return rule{}, fmt.Errorf("%w: signing ID required", ErrIdentity)
		}
		r.signing = a.SigningID
	}
	if o.Match == MatchApp || o.Match == MatchTeam {
		r.team = a.TeamID
		if r.team == "" && a.Signature.Category == appidentity.Apple {
			r.team = "*APPLE*"
		}
		if !identifier(r.team) || (r.team == "*APPLE*" && a.Signature.Category != appidentity.Apple) {
			return rule{}, fmt.Errorf("%w: team ID required; Apple sentinel requires verified Apple signing", ErrIdentity)
		}
	}
	return r, nil
}

// ComposeIdentifier joins a bundle ID and designated requirement for macOS
// privacy defaults. It checks delimiter safety, not Apple's requirement grammar.
func ComposeIdentifier(bundleID, requirement string) (string, error) {
	if !identifier(bundleID) || strings.TrimSpace(requirement) == "" || requirement != strings.TrimSpace(requirement) || strings.ContainsAny(requirement, "{}") || hasControl(requirement) {
		return "", fmt.Errorf("%w: bundle ID and designated requirement required", ErrInput)
	}
	return bundleID + " {" + requirement + "}", nil
}

// PrivacyDefaults constructs target-specific permission keys and validates every
// generated permission record. Duplicate keys and empty justifications are errors.
func PrivacyDefaults(entries []PrivacyEntry, target support.Target) (*ddm.AppSettings, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: privacy entries required", ErrInput)
	}
	defaults := make(map[string]ddm.AppSettingsAppDictionary, len(entries))
	for _, entry := range entries {
		key := entry.BundleID
		if !identifier(key) || strings.TrimSpace(entry.Permissions.OrganizationJustification) == "" {
			return nil, fmt.Errorf("%w: bundle ID and organization justification required", ErrInput)
		}
		switch target.OS {
		case support.MacOS:
			var err error
			key, err = ComposeIdentifier(entry.BundleID, entry.DesignatedRequirement)
			if err != nil {
				return nil, err
			}
		case support.IOS:
			if entry.DesignatedRequirement != "" {
				return nil, fmt.Errorf("%w: iOS privacy keys use only bundle IDs", ErrInput)
			}
		default:
			return nil, fmt.Errorf("%w: privacy identifiers require iOS or macOS", ErrInput)
		}
		if _, exists := defaults[key]; exists {
			return nil, fmt.Errorf("%w: duplicate privacy identifier %s", ErrInput, key)
		}
		defaults[key] = entry.Permissions
	}
	return validate(&ddm.AppSettings{Privacy: &ddm.AppSettingsPrivacy{PermissionDefaults: defaults}}, target)
}

func validate(payload *ddm.AppSettings, target support.Target) (*ddm.AppSettings, error) {
	if target.OS == "" || target.Version.IsZero() || (target.Channel != support.ChannelDevice && target.Channel != support.ChannelUser) {
		return nil, fmt.Errorf("%w: target OS, version and channel required", ErrInput)
	}
	if err := payload.Validate(target); err != nil {
		return nil, err
	}
	return payload, nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return new(value)
}

func identifier(value string) bool {
	return value != "" && !strings.ContainsAny(value, "{}") && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func hasControl(value string) bool { return strings.IndexFunc(value, unicode.IsControl) >= 0 }
