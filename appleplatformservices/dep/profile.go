package dep

import (
	"net/url"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/other"
)

// Length limits from the Define a Profile page, in UTF-8 characters (the
// URL limit is in URL-encoded characters).
const (
	MaxProfileNameLen  = 125
	MaxDepartmentLen   = 125
	MaxProfileURLLen   = 2000
	MaxOrgMagicLen     = 256
	MaxSupportEmailLen = 250
	MaxSupportPhoneLen = 50
)

// skipKeys is the set of valid skip_setup_items, read from the plist tags
// of the generated other.SkipKeys type so the list tracks Apple's
// other/skipkeys.yaml through the generator.
var skipKeys = loadSkipKeys()

func loadSkipKeys() map[string]struct{} {
	t := reflect.TypeFor[other.SkipKeys]()
	out := make(map[string]struct{}, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("plist")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			out[name] = struct{}{}
		}
	}
	return out
}

// SkipKeys lists the valid skip_setup_items values, sorted.
func SkipKeys() []string {
	out := make([]string, 0, len(skipKeys))
	for k := range skipKeys {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Validate applies the rules the Define a Profile page documents before a
// request is made, returning a *ProfileError carrying the code Apple would
// answer with: CONFIG_NAME_INVALID, CONFIG_URL_INVALID, MAGIC_INVALID,
// DEPARTMENT_INVALID, SUPPORT_EMAIL_INVALID, SUPPORT_PHONE_INVALID,
// FLAGS_INVALID (is_mdm_removable=false needs is_supervised=true), and
// SKIP_SETUP_ITEM_INVALID for a skip_setup_items entry outside SkipKeys.
func (p *Profile) Validate() error {
	if p == nil {
		return &ProfileError{Code: CodeConfigNameRequired, Detail: "nil profile"}
	}
	if err := checkLen(CodeConfigNameInvalid, "profile_name", p.ProfileName, MaxProfileNameLen); err != nil {
		return err
	}
	if err := p.validateURL(); err != nil {
		return err
	}
	if err := checkLen(CodeMagicInvalid, "org_magic", p.OrgMagic, MaxOrgMagicLen); err != nil {
		return err
	}
	if p.Department != "" {
		if err := checkLen(CodeDepartmentInvalid, "department", p.Department, MaxDepartmentLen); err != nil {
			return err
		}
	}
	if p.SupportEmailAddress != "" {
		if err := checkLen(CodeSupportEmailInvalid, "support_email_address", p.SupportEmailAddress, MaxSupportEmailLen); err != nil {
			return err
		}
	}
	if p.SupportPhoneNumber != "" {
		if err := checkLen(CodeSupportPhoneInvalid, "support_phone_number", p.SupportPhoneNumber, MaxSupportPhoneLen); err != nil {
			return err
		}
	}
	if p.IsMDMRemovable != nil && !*p.IsMDMRemovable && (p.IsSupervised == nil || !*p.IsSupervised) {
		return &ProfileError{Code: CodeFlagsInvalid, Detail: "is_mdm_removable=false requires is_supervised=true"}
	}
	for _, k := range p.SkipSetupItems {
		if _, ok := skipKeys[k]; !ok {
			return &ProfileError{Code: CodeSkipKeyInvalid, Detail: "unknown skip_setup_items entry " + k}
		}
	}
	return nil
}

func (p *Profile) validateURL() error {
	if p.URL == "" {
		return &ProfileError{Code: CodeConfigURLInvalid, Detail: "url is empty"}
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &ProfileError{Code: CodeConfigURLInvalid, Detail: "url is not absolute"}
	}
	// Apple counts the URL as sent (already URL-encoded); u.String()
	// percent-encodes only what an absolute URL may not carry.
	if len(u.String()) > MaxProfileURLLen {
		return &ProfileError{Code: CodeConfigURLInvalid, Detail: "url exceeds 2000 URL-encoded characters"}
	}
	return nil
}

func checkLen(code, field, value string, limit int) error {
	if value == "" {
		return &ProfileError{Code: code, Detail: field + " is empty"}
	}
	if utf8.RuneCountInString(value) > limit {
		return &ProfileError{Code: code, Detail: field + " exceeds the maximum length"}
	}
	return nil
}
