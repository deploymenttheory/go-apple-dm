package contentcache

import (
	"errors"
	"fmt"
	"time"
	"uuid"
)

// ErrInvalidReport identifies malformed or schema-invalid metrics.
var ErrInvalidReport = errors.New("contentcache: invalid report")

// Validate checks required properties and the formats and enums Apple declares.
// It adds no undocumented counter ranges or version restrictions.
func (r *Report) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: null report", ErrInvalidReport)
	}
	if r.Version == nil {
		return fmt.Errorf("%w: version is required", ErrInvalidReport)
	}
	if r.ReportDate == nil {
		return fmt.Errorf("%w: reportDate is required", ErrInvalidReport)
	}
	if r.Hostname == nil {
		return fmt.Errorf("%w: hostname is required", ErrInvalidReport)
	}
	if r.Hardware == nil {
		return fmt.Errorf("%w: hardware is required", ErrInvalidReport)
	}
	if r.ServerGUID == nil {
		return fmt.Errorf("%w: serverGUID is required", ErrInvalidReport)
	}
	if r.ReportDate != nil {
		if _, err := time.Parse(time.RFC3339Nano, *r.ReportDate); err != nil {
			return fmt.Errorf("%w: reportDate: %w", ErrInvalidReport, err)
		}
	}
	if r.CreationDate != nil {
		if _, err := time.Parse(time.RFC3339Nano, *r.CreationDate); err != nil {
			return fmt.Errorf("%w: creationDate: %w", ErrInvalidReport, err)
		}
	}
	if err := validateGUID("serverGUID", r.ServerGUID); err != nil {
		return err
	}
	if r.RegistrationState != nil {
		switch *r.RegistrationState {
		case -1, 0, 1:
		default:
			return fmt.Errorf("%w: registrationState is not an allowed value", ErrInvalidReport)
		}
	}
	if r.RegistrationStarted != nil {
		if _, err := time.Parse(time.RFC3339Nano, *r.RegistrationStarted); err != nil {
			return fmt.Errorf("%w: registrationStarted: %w", ErrInvalidReport, err)
		}
	}
	if r.TetheratorStatus != nil {
		switch *r.TetheratorStatus {
		case -1, 0, 1:
		default:
			return fmt.Errorf("%w: tetheratorStatus is not an allowed value", ErrInvalidReport)
		}
	}
	if r.ParentSelectionPolicy != nil {
		switch *r.ParentSelectionPolicy {
		case "first-available", "random", "round-robin", "sticky-available", "url-path-hash":
		default:
			return fmt.Errorf("%w: parentSelectionPolicy is not an allowed value", ErrInvalidReport)
		}
	}
	for i, p := range r.Parents {
		if err := validateGUID(fmt.Sprintf("parents[%d].guid", i), p.GUID); err != nil {
			return err
		}
	}
	for i, p := range r.Peers {
		if err := validateGUID(fmt.Sprintf("peers[%d].guid", i), p.GUID); err != nil {
			return err
		}
	}
	return nil
}

func validateGUID(path string, value *string) error {
	if value == nil {
		return nil
	}
	if _, err := uuid.Parse(*value); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidReport, path, err)
	}
	return nil
}
