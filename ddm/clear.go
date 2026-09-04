package ddm

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// ClearEnrollment forgets everything the engine holds for one enrollment:
// set and declaration assignments, the snapshot, status, and pending
// changes. Declarations and sets themselves are untouched.
func (e *Engine) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return e.store.Update(ctx, func(tx Tx) error { return tx.ClearEnrollment(ctx, id) })
}
