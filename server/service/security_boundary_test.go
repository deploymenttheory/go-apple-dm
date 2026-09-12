package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestPinWarningNeverCreatesRetroactiveAssociation(t *testing.T) {
	h := newHarness(t, service.Config{Pinning: service.PinWarn})
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	if _, err := h.core.Checkin(ctx, req(nil), authenticate(t, id.ID)); err != nil {
		t.Fatal(err)
	}
	for _, cert := range []*mdm.Request{req(nil), req(h.cert), req(h.cert2)} {
		if _, err := h.core.Checkin(ctx, cert, tokenUpdate(t, id.ID, nil)); err != nil {
			t.Fatal(err)
		}
		if pin, err := h.store.CertHash(ctx, id); err != nil || pin != "" {
			t.Fatal("warning pinned an identity", pin, err)
		}
	}
	if err := h.store.AssociateCert(ctx, id, cms.Fingerprint(h.cert), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.core.Connect(
		ctx,
		req(h.cert2),
		response(id.ID, "", mdm.StatusIdle),
	); err != nil {
		t.Fatal(err)
	}
	pin, _ := h.store.CertHash(ctx, id)
	if pin != cms.Fingerprint(h.cert) {
		t.Fatal("warning changed pin")
	}
}

func TestTargetLookupFailureDoesNotQueueCommands(t *testing.T) {
	backing := inmem.New()
	st := &storagetest.Failing{Store: backing, Fail: map[string]error{}}
	core, err := service.New(service.Config{Store: st, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	if err = backing.Import(
		t.Context(),
		storage.EnrollmentExport{
			Enrollment: storage.Enrollment{
				ID:      id,
				Enabled: true,
				Device:  storage.DeviceInfo{ProductName: "Mac15,3", OSVersion: "26.0"},
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	st.Fail["Get"] = errDB
	if _, err = core.Enqueue(
		t.Context(),
		[]mdm.EnrollmentID{id},
		newCmd(t, &commands.DeviceInformation{}),
		storage.EnqueueOptions{},
	); !errors.Is(
		err,
		errDB,
	) {
		t.Fatal(err)
	}
	if cmd, err := backing.Next(t.Context(), id, false, t0); err != nil || cmd != nil {
		t.Fatal("target failure queued command", cmd, err)
	}
}

type replacementFaultStore struct {
	*inmem.Store
	fail string
}

func (s *replacementFaultStore) TransitionReplacement(
	ctx context.Context,
	id mdm.EnrollmentID,
	c storage.ReplacementChange,
) (*storage.Replacement, error) {
	if s.fail == c.Op {
		return nil, errDB
	}
	return s.Store.TransitionReplacement(ctx, id, c)
}

func TestReplacementStorageFailuresDoNotAuthenticateOrDeliver(t *testing.T) {
	h := newHarness(t, service.Config{})
	st := &replacementFaultStore{Store: h.store}
	core, err := service.New(
		service.Config{Store: st, Clock: clock.NewFake(t0), EnableReplacements: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	enroll(t, h, id.ID)
	command := newCmd(t, &commands.InstallProfile{Payload: []byte("replacement")})
	x := &storage.Replacement{
		ID:        command.UUID,
		Method:    "acme",
		OldHash:   cms.Fingerprint(h.cert),
		Command:   *command,
		ExpiresAt: t0.Add(10 * time.Minute),
	}
	if _, err = st.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{Op: "begin", Begin: x, At: t0},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = st.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{
			Op:     "issue",
			ID:     x.ID,
			Method: "acme",
			Hash:   cms.Fingerprint(h.cert2),
			At:     t0,
		},
	); err != nil {
		t.Fatal(err)
	}

	authority, err := testpki.NewCA("other issuer")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := authority.Issue("other device", t0.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.Connect(
		ctx,
		req(foreign.Cert),
		response(id.ID, "", mdm.StatusIdle),
	); !errors.Is(
		err,
		service.ErrCertMismatch,
	) {
		t.Fatal("foreign identity obtained replacement", err)
	}
	if _, err = core.Checkin(
		ctx,
		req(foreign.Cert),
		tokenUpdate(t, id.ID, nil),
	); !errors.Is(
		err,
		service.ErrCertMismatch,
	) {
		t.Fatal("foreign identity updated replacement", err)
	}
	if _, err = core.Connect(
		ctx,
		req(h.cert2),
		response(id.ID, "unrelated-command", mdm.StatusAcknowledged),
	); !errors.Is(
		err,
		service.ErrCertMismatch,
	) {
		t.Fatal("candidate completed unrelated command", err)
	}
	for _, operation := range []string{"read", "deliver", "authenticate"} {
		st.fail = operation
		if operation == "authenticate" {
			_, err = core.Checkin(ctx, req(h.cert2), authenticate(t, id.ID))
		} else {
			_, err = core.Connect(ctx, req(h.cert), response(id.ID, "", mdm.StatusIdle))
		}
		if !errors.Is(err, errDB) {
			t.Fatal(operation, err)
		}
	}
	st.fail = ""
	after, err := st.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: t0})
	if err != nil || after.Authenticated || after.Delivered {
		t.Fatal("failed transition changed replacement", after, err)
	}
}

func TestReplacementRequiresAtomicStorageSupport(t *testing.T) {
	st := struct{ storage.Store }{inmem.New()}
	if _, err := service.New(service.Config{Store: st, EnableReplacements: true}); err == nil {
		t.Fatal("replacement enabled without an atomic replacement store")
	}
}
