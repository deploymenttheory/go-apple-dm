// Package profile composes, signs, and parses Apple configuration
// profiles (.mobileconfig). Payload bodies are the generated types in
// schema/profiles; this package supplies the top-level envelope, the
// common payload keys, stable identifiers, and CMS signing (decision
// record 0009).
//
// Apple documentation:
// https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys
package profile

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/internal/uuid"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/schema/profiles"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// Errors returned by this package.
var (
	ErrInvalid = errors.New("profile: invalid")
	ErrParse   = errors.New("profile: parse")
)

// PayloadTypeConfiguration is the top-level PayloadType of every profile.
const PayloadTypeConfiguration = "Configuration"

// Scope values for PayloadScope.
const (
	ScopeSystem = "System"
	ScopeUser   = "User"
)

// NewUUID returns a fresh upper-case UUID (version 7) for PayloadUUID
// values. Callers keep it with the profile so updates stay in place.
func NewUUID() string { return strings.ToUpper(uuid.NewV7().String()) }

// Payload is one entry in PayloadContent: the common keys plus a typed body.
type Payload struct {
	Identifier   string
	UUID         string
	Version      int64 // default 1
	DisplayName  string
	Description  string
	Organization string
	// Content is the payload body, a type from schema/profiles or a Raw.
	Content profiles.Payload
}

// Profile is a configuration profile.
type Profile struct {
	Identifier        string
	UUID              string
	Version           int64 // default 1
	DisplayName       string
	Description       string
	Organization      string
	Scope             string
	RemovalDisallowed bool
	Payloads          []Payload
	// Extra top-level keys emitted verbatim (ConsentText, RemovalDate...).
	// Reserved keys set by the builder are ignored here.
	Extra map[string]any
}

// Raw is a payload whose body is kept as plist keys, for payload types
// with no generated type or where the registry cannot pick one.
type Raw struct {
	Type string
	Keys map[string]any
}

// PayloadTypeName implements profiles.Payload.
func (r *Raw) PayloadTypeName() string { return r.Type }

// SchemaPath implements profiles.Payload.
func (*Raw) SchemaPath() string { return "" }

// Validate implements profiles.Payload; raw payloads are not validated.
func (*Raw) Validate(support.Target) error { return nil }

// common are the per-payload keys the builder controls. PayloadContent is
// not among them: SCEP, certificate, and other payloads use it for their
// body.
var common = map[string]bool{
	"PayloadType": true, "PayloadVersion": true, "PayloadIdentifier": true, "PayloadUUID": true,
	"PayloadDisplayName": true, "PayloadDescription": true, "PayloadOrganization": true,
}

// reserved are the top-level keys the builder controls.
var reserved = map[string]bool{
	"PayloadType": true, "PayloadVersion": true, "PayloadIdentifier": true, "PayloadUUID": true,
	"PayloadDisplayName": true, "PayloadDescription": true, "PayloadOrganization": true,
	"PayloadContent": true, "PayloadScope": true, "PayloadRemovalDisallowed": true,
}

// Validate checks the envelope and every payload against the schema for
// the target. Errors are collected, not first-fail.
func (p *Profile) Validate(t support.Target) error {
	var errs []error
	if p.Identifier == "" {
		errs = append(errs, fmt.Errorf("%w: PayloadIdentifier is required", ErrInvalid))
	}
	if p.UUID == "" {
		errs = append(errs, fmt.Errorf("%w: PayloadUUID is required", ErrInvalid))
	}
	if p.Scope != "" && p.Scope != ScopeSystem && p.Scope != ScopeUser {
		errs = append(errs, fmt.Errorf("%w: PayloadScope %q", ErrInvalid, p.Scope))
	}
	seen := map[string]string{}
	for i, pl := range p.Payloads {
		where := fmt.Sprintf("PayloadContent[%d]", i)
		if pl.Content == nil {
			errs = append(errs, fmt.Errorf("%w: %s: nil content", ErrInvalid, where))
			continue
		}
		if pl.Identifier == "" || pl.UUID == "" {
			errs = append(errs, fmt.Errorf("%w: %s (%s): PayloadIdentifier and PayloadUUID are required", ErrInvalid, where, pl.Content.PayloadTypeName()))
		}
		if prev, dup := seen[pl.UUID]; dup && pl.UUID != "" {
			errs = append(errs, fmt.Errorf("%w: %s: PayloadUUID %s already used by %s", ErrInvalid, where, pl.UUID, prev))
		}
		seen[pl.UUID] = where
		if err := pl.Content.Validate(t); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s (%s): %w", ErrInvalid, where, pl.Content.PayloadTypeName(), err))
		}
	}
	return errors.Join(errs...)
}

// Map renders the profile as plist-ready keys.
func (p *Profile) Map() (map[string]any, error) {
	top := map[string]any{}
	for k, v := range p.Extra {
		if !reserved[k] {
			top[k] = v
		}
	}
	top["PayloadType"] = PayloadTypeConfiguration
	top["PayloadVersion"] = orOne(p.Version)
	top["PayloadIdentifier"] = p.Identifier
	top["PayloadUUID"] = p.UUID
	setIf(top, "PayloadDisplayName", p.DisplayName)
	setIf(top, "PayloadDescription", p.Description)
	setIf(top, "PayloadOrganization", p.Organization)
	setIf(top, "PayloadScope", p.Scope)
	if p.RemovalDisallowed {
		top["PayloadRemovalDisallowed"] = true
	}
	content := make([]any, 0, len(p.Payloads))
	for i, pl := range p.Payloads {
		m, err := pl.Map()
		if err != nil {
			return nil, fmt.Errorf("PayloadContent[%d]: %w", i, err)
		}
		content = append(content, m)
	}
	top["PayloadContent"] = content
	return top, nil
}

// Map renders one payload with its common keys.
func (pl *Payload) Map() (map[string]any, error) {
	if pl.Content == nil {
		return nil, fmt.Errorf("%w: nil content", ErrInvalid)
	}
	var m map[string]any
	if r, ok := pl.Content.(*Raw); ok {
		m = maps.Clone(r.Keys)
		if m == nil {
			m = map[string]any{}
		}
	} else {
		data, err := plist.Marshal(pl.Content)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if err := plist.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	m["PayloadType"] = pl.Content.PayloadTypeName()
	m["PayloadVersion"] = orOne(pl.Version)
	m["PayloadIdentifier"] = pl.Identifier
	m["PayloadUUID"] = pl.UUID
	setIf(m, "PayloadDisplayName", pl.DisplayName)
	setIf(m, "PayloadDescription", pl.Description)
	setIf(m, "PayloadOrganization", pl.Organization)
	return m, nil
}

// Marshal renders the profile as an XML plist.
func (p *Profile) Marshal() ([]byte, error) {
	m, err := p.Map()
	if err != nil {
		return nil, err
	}
	data, err := plist.MarshalIndent(m, "\t")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return data, nil
}

// Sign renders and signs the profile with an attached CMS signature, the
// form devices show as "Verified".
func (p *Profile) Sign(cert *x509.Certificate, key crypto.Signer) ([]byte, error) {
	data, err := p.Marshal()
	if err != nil {
		return nil, err
	}
	signed, err := cms.SignAttached(data, cert, key)
	if err != nil {
		return nil, fmt.Errorf("profile: sign: %w", err)
	}
	return signed, nil
}

// Resolver picks the Go type for a payload. It returns nil to keep the
// payload as Raw.
type Resolver func(payloadType string, keys map[string]any) profiles.Payload

// ParseOptions configure Parse.
type ParseOptions struct {
	// Verify is applied when the data is CMS-signed. Roots nil means any
	// embedded chain is accepted; see cms.VerifyOptions.
	Verify cms.VerifyOptions
	// RequireSignature rejects unsigned input.
	RequireSignature bool
	// Resolve overrides the registry lookup. The default resolves payload
	// types with exactly one generated type and keeps the rest raw.
	Resolve Resolver
	// MaxBytes bounds the plist (default plist.Decoder default).
	MaxBytes int
}

// Parsed is the result of Parse.
type Parsed struct {
	Profile *Profile
	// Signer is the certificate that signed the profile, nil when unsigned.
	Signer *x509.Certificate
	// Plist is the decoded (unsigned) plist bytes.
	Plist []byte
}

// DefaultResolver resolves a payload type through schema/profiles.
func DefaultResolver(payloadType string, _ map[string]any) profiles.Payload {
	entries := profiles.ByID(payloadType)
	if len(entries) != 1 {
		return nil
	}
	return entries[0].New()
}

// Parse reads a signed or unsigned profile back into typed payloads.
func Parse(data []byte, o ParseOptions) (*Parsed, error) {
	out := &Parsed{Plist: data}
	if cms.IsSigned(data) {
		content, signer, err := cms.VerifyAttached(data, o.Verify)
		if err != nil {
			return nil, fmt.Errorf("%w: signature: %w", ErrParse, err)
		}
		out.Plist, out.Signer = content, signer
	} else if o.RequireSignature {
		return nil, fmt.Errorf("%w: unsigned profile rejected", ErrParse)
	}
	var top map[string]any
	if err := (plist.Decoder{MaxBytes: o.MaxBytes}).Unmarshal(out.Plist, &top); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	if t, _ := top["PayloadType"].(string); t != PayloadTypeConfiguration {
		return nil, fmt.Errorf("%w: top-level PayloadType %q, want %q", ErrParse, t, PayloadTypeConfiguration)
	}
	resolve := o.Resolve
	if resolve == nil {
		resolve = DefaultResolver
	}
	p := &Profile{
		Identifier:   str(top, "PayloadIdentifier"),
		UUID:         str(top, "PayloadUUID"),
		Version:      num(top, "PayloadVersion"),
		DisplayName:  str(top, "PayloadDisplayName"),
		Description:  str(top, "PayloadDescription"),
		Organization: str(top, "PayloadOrganization"),
		Scope:        str(top, "PayloadScope"),
	}
	p.RemovalDisallowed, _ = top["PayloadRemovalDisallowed"].(bool)
	for k, v := range top {
		if !reserved[k] {
			if p.Extra == nil {
				p.Extra = map[string]any{}
			}
			p.Extra[k] = v
		}
	}
	items, _ := top["PayloadContent"].([]any)
	for i, it := range items {
		keys, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: PayloadContent[%d] is not a dictionary", ErrParse, i)
		}
		pl, err := parsePayload(keys, resolve)
		if err != nil {
			return nil, fmt.Errorf("%w: PayloadContent[%d]: %w", ErrParse, i, err)
		}
		p.Payloads = append(p.Payloads, pl)
	}
	out.Profile = p
	return out, nil
}

func parsePayload(keys map[string]any, resolve Resolver) (Payload, error) {
	typ := str(keys, "PayloadType")
	if typ == "" {
		return Payload{}, errors.New("missing PayloadType")
	}
	pl := Payload{
		Identifier:   str(keys, "PayloadIdentifier"),
		UUID:         str(keys, "PayloadUUID"),
		Version:      num(keys, "PayloadVersion"),
		DisplayName:  str(keys, "PayloadDisplayName"),
		Description:  str(keys, "PayloadDescription"),
		Organization: str(keys, "PayloadOrganization"),
	}
	body := map[string]any{}
	for k, v := range keys {
		if !common[k] {
			body[k] = v
		}
	}
	typed := resolve(typ, keys)
	if typed == nil {
		pl.Content = &Raw{Type: typ, Keys: body}
		return pl, nil
	}
	data, err := plist.Marshal(body)
	if err != nil {
		return Payload{}, fmt.Errorf("%s: %w", typ, err)
	}
	if err := plist.Unmarshal(data, typed); err != nil {
		return Payload{}, fmt.Errorf("%s: %w", typ, err)
	}
	pl.Content = typed
	return pl, nil
}

// Find returns the first payload whose content has the type T.
func Find[T profiles.Payload](p *Profile) (T, bool) {
	for _, pl := range p.Payloads {
		if c, ok := pl.Content.(T); ok {
			return c, true
		}
	}
	var zero T
	return zero, false
}

// FindUUID returns the payload with the PayloadUUID.
func (p *Profile) FindUUID(u string) (*Payload, bool) {
	for i := range p.Payloads {
		if strings.EqualFold(p.Payloads[i].UUID, u) {
			return &p.Payloads[i], true
		}
	}
	return nil, false
}

func orOne(v int64) int64 {
	if v == 0 {
		return 1
	}
	return v
}

func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case uint64:
		return int64(v) // #nosec G115 -- plist integers fit
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
