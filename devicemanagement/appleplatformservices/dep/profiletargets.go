package dep

import (
	"fmt"
	"maps"
	"strings"
	"unicode"
)

// DesiredProfile resolves an explicit serial choice, the account default, then
// no action (empty). It does not infer policy from device family or OS.
func (a *Account) DesiredProfile(serial string) string {
	if uuid := a.ProfileTargets[serial]; uuid != "" {
		return uuid
	}
	return a.ProfileUUID
}

// ProfileTargetRevision validates desired state and returns the store-owned
// revision for a write. previous is nil when creating an account. Stores call
// this while holding the account lock; caller-supplied revisions are ignored.
func ProfileTargetRevision(previous, next *Account) (int64, error) {
	for serial, uuid := range next.ProfileTargets {
		if serial == "" || uuid == "" || strings.TrimSpace(serial) != serial || strings.ContainsFunc(serial, unicode.IsControl) {
			return 0, fmt.Errorf("%w: profile targets require serial and UUID", ErrInvalid)
		}
	}
	if previous == nil {
		return 0, nil
	}
	revision := previous.ProfileRevision
	if previous.ProfileUUID != next.ProfileUUID || !maps.Equal(previous.ProfileTargets, next.ProfileTargets) {
		revision++
	}
	return revision, nil
}

// UsesProfile reports whether a stored definition is selected as the account
// default or by an explicit serial. It does not inspect observed Apple state.
func (a *Account) UsesProfile(uuid string) bool {
	if uuid == "" {
		return false
	}
	if a.ProfileUUID == uuid {
		return true
	}
	for _, target := range a.ProfileTargets {
		if target == uuid {
			return true
		}
	}
	return false
}
