package profilelint

import (
	"crypto/x509"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// Options names the target and optional signature/trust requirements.
type Options struct {
	Target           support.Target
	Roots            *x509.CertPool
	RequireSignature bool
}

// Issue contains locations and rules, never the offending value.
type Issue struct {
	Path     string `json:"path"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

// Report distinguishes signature integrity, trust and schema completeness.
type Report struct {
	Signature string  `json:"signature"`
	Trust     string  `json:"trust"`
	Issues    []Issue `json:"issues"`
}

func (r *Report) add(path, severity, rule, message string) {
	r.Issues = append(r.Issues, Issue{path, severity, rule, message})
}

// Inspect validates a bounded input using only compiled schemas and local roots.
func Inspect(data []byte, o Options) Report {
	r := Report{Signature: "unsigned", Trust: "not-checked", Issues: []Issue{}}
	if len(data) > plist.DefaultMaxBytes {
		r.add("", "error", "size", "profile exceeds 4 MiB")
		return r
	}
	plain, ok := r.verify(data, o)
	if !ok {
		return r
	}
	var raw map[string]any
	if err := plist.Unmarshal(plain, &raw); err != nil {
		r.add("", "error", "parse", "cannot decode the profile property list")
		return r
	}
	var top profiles.TopLevel
	if err := plist.Unmarshal(plain, &top); err != nil {
		r.add("", "error", "type", "profile envelope has an invalid field type")
		return r
	}
	// Apple's ANY placeholder models each dictionary itself, not a literal
	// "ANY" plist key. Populate it before invoking generated validation.
	items, _ := raw["PayloadContent"].([]any)
	for i := range top.PayloadContent {
		if i < len(items) {
			top.PayloadContent[i].ANY = items[i]
		}
	}
	r.validate("", top.Validate(o.Target))
	r.walk(raw, reflect.TypeFor[profiles.TopLevel](), "TopLevel", "", o.Target)
	if _, encrypted := raw["EncryptedPayloadContent"]; encrypted {
		r.add(
			"EncryptedPayloadContent",
			"unvalidated",
			"encrypted",
			"encrypted payload contents require a decryption key",
		)
		return r
	}
	parsed, err := profile.Parse(plain, profile.ParseOptions{})
	if err != nil {
		r.add(
			"PayloadContent",
			"error",
			"parse",
			"cannot decode profile payloads; check their field types and envelope",
		)
		return r
	}
	envelope := *parsed.Profile
	envelope.Payloads = nil
	r.validate("", envelope.Validate(o.Target))
	seenUUID, seenID := map[string]bool{}, map[string]bool{}
	for i, payload := range parsed.Profile.Payloads {
		path := fmt.Sprintf("PayloadContent[%d](%s)", i, payload.Content.PayloadTypeName())
		if i >= len(items) {
			continue
		}
		keys, _ := items[i].(map[string]any)
		common, body := splitKeys(keys)
		encoded, err := plist.Marshal(common)
		var identity profiles.CommonPayloadKeys
		if err != nil || plist.Unmarshal(encoded, &identity) != nil {
			r.add(path, "error", "type", "payload identity has an invalid field type")
		} else {
			r.validate(path, identity.Validate(o.Target))
		}
		if seenUUID[strings.ToLower(payload.UUID)] || seenID[payload.Identifier] {
			r.add(
				path,
				"error",
				"duplicate",
				"payload UUID and identifier must be unique within the profile",
			)
		}
		seenUUID[strings.ToLower(payload.UUID)], seenID[payload.Identifier] = true, true
		r.walk(
			common,
			reflect.TypeFor[profiles.CommonPayloadKeys](),
			"CommonPayloadKeys",
			path,
			o.Target,
		)
		if _, unknown := payload.Content.(*profile.Raw); unknown {
			r.add(path, "unvalidated", "unknown", "unknown or ambiguous payload type")
			continue
		}
		r.validate(path, payload.Content.Validate(o.Target))
		typ := reflect.TypeOf(payload.Content).Elem()
		r.checkSupport(typ.Name(), path, o.Target)
		r.walk(body, typ, typ.Name(), path, o.Target)
	}
	sort.SliceStable(r.Issues, func(i, j int) bool { return r.Issues[i].Path < r.Issues[j].Path })
	return r
}

func (r *Report) verify(data []byte, o Options) ([]byte, bool) {
	if !cms.IsSigned(data) {
		if o.RequireSignature {
			r.add("", "error", "signature", "a signed profile is required")
			return nil, false
		}
		return data, true
	}
	content, _, err := cms.VerifyAttached(data, cms.VerifyOptions{})
	if err != nil {
		r.Signature = "invalid"
		r.add("", "error", "signature", "CMS signature verification failed")
		return nil, false
	}
	r.Signature = "valid"
	if o.Roots != nil {
		if _, _, err := cms.VerifyAttached(data, cms.VerifyOptions{Roots: o.Roots}); err != nil {
			r.Trust = "untrusted"
			r.add("", "error", "trust", "signer does not verify against the supplied roots")
		} else {
			r.Trust = "trusted"
		}
	}
	return content, true
}

func (r *Report) validate(path string, err error) {
	if err == nil {
		return
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			r.validate(path, child)
		}
		return
	}
	var entries validation.Errors
	if errors.As(err, &entries) {
		for _, issue := range entries {
			r.add(join(path, issue.Path), "error", issue.Rule, "does not satisfy the schema rule")
		}
		return
	}
	r.add(
		path,
		"error",
		"profile",
		"invalid profile envelope or duplicate/missing payload identity",
	)
}

func splitKeys(raw map[string]any) (map[string]any, map[string]any) {
	common, body := map[string]any{}, map[string]any{}
	typ := reflect.TypeFor[profiles.CommonPayloadKeys]()
	for k, v := range raw {
		if _, ok := fieldType(typ, k); ok {
			common[k] = v
		} else {
			body[k] = v
		}
	}
	return common, body
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func fieldType(typ reflect.Type, key string) (reflect.Type, bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if strings.Split(field.Tag.Get("plist"), ",")[0] == key {
			return field.Type, true
		}
	}
	return nil, false
}
