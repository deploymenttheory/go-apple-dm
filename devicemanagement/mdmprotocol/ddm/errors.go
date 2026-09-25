package ddm

import (
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// Errors shared by the engine and every store backend.
//
// The conditions a caller reacts to point at the catalogue, so each one also matches
// its kind and carries a stable code wherever it surfaces. Comparison against these
// variables is unchanged. The remaining errors describe a failure inside a supplied
// component and have no classification of their own.
var (
	ErrNotFound           = fault.DDMNotFound
	ErrConflict           = fault.DDMConflict
	ErrInvalid            = fault.DDMInvalid
	ErrUnknownType        = fault.DDMUnknownType
	ErrInvalidDeclaration = fault.DDMDeclarationInvalid
	ErrBadEndpoint        = fault.DeviceDDMEndpointMalformed
	ErrStatusTooLarge     = fault.DeviceDDMStatusTooLarge
	ErrStatusMalformed    = fault.DeviceDDMStatusMalformed
	ErrResolver           = fault.NewOperator(fault.Internal, "the membership resolver failed")
	ErrExpander           = fault.NewOperator(fault.Internal, "the expander failed")
	ErrNotifier           = fault.NewOperator(fault.Unavailable, "the notifier failed")
)
