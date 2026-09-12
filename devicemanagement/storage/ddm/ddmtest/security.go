package ddmtest

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// RunIdentitySuite verifies permanent channel and parent identity boundaries.
func RunIdentitySuite(t *testing.T, f Factory) {
	s := f(t)
	ctx := t.Context()
	id := User(1, "alice")
	if err := s.PutSnapshot(
		ctx,
		&ddm.Snapshot{ID: id, RefreshedAt: t0, TokenChangedAt: t0},
	); err != nil {
		t.Fatal(err)
	}
	wrong := id
	wrong.ParentID = Device(2).ID
	channel := id
	channel.Channel = mdm.ChannelSharedIPadUser
	for _, bad := range []mdm.EnrollmentID{wrong, channel} {
		if _, err := s.Snapshot(
			ctx,
			bad,
		); !errors.Is(err, ddm.ErrNotFound) &&
			!errors.Is(err, ddm.ErrConflict) {
			t.Fatal("mismatched read", err)
		}
		if err := s.PutSnapshot(
			ctx,
			&ddm.Snapshot{ID: bad},
		); !errors.Is(err, ddm.ErrConflict) &&
			!errors.Is(err, ddm.ErrNotFound) {
			t.Fatal("mismatched write", err)
		}
	}
	if err := s.ClearEnrollment(ctx, id); err != nil {
		t.Fatal(err)
	}
	canonical, err := s.EnrollmentIdentity(ctx, id.ID)
	if err != nil || canonical != id {
		t.Fatal("identity removed by cleanup", canonical, err)
	}
}
