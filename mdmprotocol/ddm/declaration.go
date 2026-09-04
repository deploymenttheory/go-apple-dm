package ddm

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/canonjson"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

// declarationEnvelope is the top level of an uploaded declaration.
type declarationEnvelope struct {
	Type        string         `json:"Type"`
	Identifier  string         `json:"Identifier"`
	ServerToken string         `json:"ServerToken,omitempty"`
	Payload     jsontext.Value `json:"Payload,omitempty"`
}

// ParseDeclaration validates an uploaded declaration and derives its
// canonical bytes and ServerToken (decision record 0019). The upload's own
// ServerToken is ignored: tokens are derived, never authored. Type must be
// one of the standalone families in schema/ddm and the generated Validate
// must pass for target.
func ParseDeclaration(raw []byte, target support.Target) (*Declaration, error) {
	var env declarationEnvelope
	if err := json.Unmarshal(raw, &env, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDeclaration, err)
	}
	if err := validIdentifier(env.Identifier); err != nil {
		return nil, err
	}
	if env.Type == "" {
		return nil, fmt.Errorf("%w: Type is required", ErrInvalidDeclaration)
	}
	entry, err := lookupType(env.Type)
	if err != nil {
		return nil, err
	}
	payload := env.Payload
	if len(payload) == 0 {
		payload = jsontext.Value("{}")
	}
	typed := entry.New()
	if err := json.Unmarshal(payload, typed); err != nil {
		return nil, fmt.Errorf("%w: %s payload: %w", ErrInvalidDeclaration, env.Type, err)
	}
	if err := typed.Validate(target); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidDeclaration, env.Type, err)
	}
	canonical, err := canonicalDeclaration(env.Identifier, env.Type, payload)
	if err != nil {
		return nil, err
	}
	return &Declaration{
		Identifier:  env.Identifier,
		Type:        env.Type,
		Kind:        entry.Kind,
		ServerToken: TokenFor(canonical),
		Canonical:   canonical,
	}, nil
}

// canonicalDeclaration renders {"Identifier","Payload","Type"} in JCS form.
func canonicalDeclaration(identifier, typ string, payload jsontext.Value) ([]byte, error) {
	out, err := canonjson.Marshal(map[string]any{"Identifier": identifier, "Payload": payload, "Type": typ})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDeclaration, err)
	}
	return out, nil
}

func validIdentifier(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: Identifier is required", ErrInvalidDeclaration)
	case len(s) > MaxIdentifierBytes:
		return fmt.Errorf("%w: Identifier longer than %d bytes", ErrInvalidDeclaration, MaxIdentifierBytes)
	case strings.Contains(s, "/"):
		return fmt.Errorf("%w: Identifier must not contain '/'", ErrInvalidDeclaration)
	}
	return nil
}

// lookupType finds the registry entry for a wire type among the four
// standalone families. Credential sub-schemas and the base type are not
// declarations a device can fetch.
func lookupType(typ string) (schemaddm.Entry, error) {
	for _, entry := range schemaddm.ByID(typ) {
		for _, k := range StandaloneKinds {
			if entry.Kind == k {
				return entry, nil
			}
		}
	}
	return schemaddm.Entry{}, fmt.Errorf("%w: %q", ErrUnknownType, typ)
}

// splitCanonical recovers the three members of a canonical declaration.
func splitCanonical(canonical []byte) (declarationEnvelope, error) {
	var env declarationEnvelope
	if err := json.Unmarshal(canonical, &env); err != nil {
		return env, fmt.Errorf("%w: stored declaration: %w", ErrInvalidDeclaration, err)
	}
	return env, nil
}

// RenderDeclaration returns the wire form of a declaration: its canonical
// members plus the ServerToken the device must echo back.
func RenderDeclaration(canonical []byte, serverToken string) ([]byte, error) {
	env, err := splitCanonical(canonical)
	if err != nil {
		return nil, err
	}
	payload := env.Payload
	if len(payload) == 0 {
		payload = jsontext.Value("{}")
	}
	out, err := canonjson.Marshal(map[string]any{"Identifier": env.Identifier, "Payload": payload, "ServerToken": serverToken, "Type": env.Type})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDeclaration, err)
	}
	return out, nil
}
