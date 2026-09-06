package ddm

import "errors"

// Errors shared by the engine and every store backend.
var (
	ErrNotFound           = errors.New("ddm: not found")
	ErrConflict           = errors.New("ddm: conflict")
	ErrInvalid            = errors.New("ddm: invalid argument")
	ErrUnknownType        = errors.New("ddm: unknown declaration type")
	ErrInvalidDeclaration = errors.New("ddm: declaration failed validation")
	ErrBadEndpoint        = errors.New("ddm: malformed endpoint")
	ErrStatusTooLarge     = errors.New("ddm: status report exceeds limit")
	ErrStatusMalformed    = errors.New("ddm: malformed status report")
	ErrResolver           = errors.New("ddm: membership resolver failed")
	ErrExpander           = errors.New("ddm: expander failed")
	ErrNotifier           = errors.New("ddm: notifier")
)
