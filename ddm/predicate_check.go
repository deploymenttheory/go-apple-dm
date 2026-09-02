package ddm

import (
	json "encoding/json/v2"
	"fmt"

	"github.com/deploymenttheory/go-apple-mdm/ddm/predicate"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
)

// validatePredicate rejects an activation whose Predicate would not parse,
// so a device is never handed a predicate it cannot evaluate (Fleet
// FB24193230 wedged devices this way).
func (e *Engine) validatePredicate(d *Declaration) error {
	if d.Type != schemaddm.DeclarationTypeActivationSimple {
		return nil
	}
	env, err := splitCanonical(d.Canonical)
	if err != nil {
		return err
	}
	var act schemaddm.ActivationSimple
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &act); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidDeclaration, err)
		}
	}
	if act.Predicate == nil || *act.Predicate == "" {
		return nil
	}
	if err := predicate.Validate(*act.Predicate); err != nil {
		return fmt.Errorf("%w: predicate: %w", ErrInvalidDeclaration, err)
	}
	return nil
}
