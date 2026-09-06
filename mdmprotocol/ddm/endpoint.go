package ddm

import (
	"fmt"
	"strings"

	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// Op is the operation a DeclarativeManagement check-in requests.
type Op int

// Operations, in the order Apple lists the Endpoint values.
const (
	OpTokens Op = iota + 1
	OpDeclarationItems
	OpDeclaration
	OpStatus
)

// String returns the wire name of the operation.
func (o Op) String() string {
	switch o {
	case OpTokens:
		return "tokens"
	case OpDeclarationItems:
		return "declaration-items"
	case OpDeclaration:
		return "declaration"
	case OpStatus:
		return "status"
	default:
		return "unknown"
	}
}

// Endpoint is a parsed DeclarativeManagement Endpoint value.
type Endpoint struct {
	Op Op
	// Kind and Identifier are set for OpDeclaration only.
	Kind       schemaddm.Kind
	Identifier string
}

// MaxIdentifierBytes is Apple's guidance for Identifier and ServerToken
// values ("should not exceed 64 octets").
const MaxIdentifierBytes = 64

// ParseEndpoint parses "tokens", "declaration-items", "status", or
// "declaration/<kind>/<identifier>" where kind is activation, asset,
// configuration, or management. Anything else is ErrBadEndpoint.
func ParseEndpoint(s string) (Endpoint, error) {
	switch s {
	case "tokens":
		return Endpoint{Op: OpTokens}, nil
	case "declaration-items":
		return Endpoint{Op: OpDeclarationItems}, nil
	case "status":
		return Endpoint{Op: OpStatus}, nil
	}
	rest, ok := strings.CutPrefix(s, "declaration/")
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: %q", ErrBadEndpoint, s)
	}
	kind, identifier, ok := strings.Cut(rest, "/")
	if !ok || identifier == "" || strings.Contains(identifier, "/") {
		return Endpoint{}, fmt.Errorf("%w: %q", ErrBadEndpoint, s)
	}
	k, err := ParseKind(kind)
	if err != nil {
		return Endpoint{}, fmt.Errorf("%w: %q: %w", ErrBadEndpoint, s, err)
	}
	if len(identifier) > MaxIdentifierBytes {
		return Endpoint{}, fmt.Errorf("%w: identifier longer than %d bytes", ErrBadEndpoint, MaxIdentifierBytes)
	}
	return Endpoint{Op: OpDeclaration, Kind: k, Identifier: identifier}, nil
}

// StandaloneKinds are the declaration families a device fetches by name.
var StandaloneKinds = []schemaddm.Kind{schemaddm.KindActivation, schemaddm.KindAsset, schemaddm.KindConfiguration, schemaddm.KindManagement}

// ParseKind accepts the four standalone declaration kinds.
func ParseKind(s string) (schemaddm.Kind, error) {
	for _, k := range StandaloneKinds {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("%w: kind %q", ErrInvalid, s)
}

// String renders the endpoint back to its wire form.
func (e Endpoint) String() string {
	if e.Op == OpDeclaration {
		return "declaration/" + string(e.Kind) + "/" + e.Identifier
	}
	return e.Op.String()
}
