package replycerts_test

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/replycerts"
)

func rotation(t *testing.T, kind, uuid string) *mdm.Command {
	t.Helper()
	cmd, err := mdm.NewCommand(
		&commands.RotateFileVaultKey{
			KeyType: kind,
			FileVaultUnlock: commands.RotateFileVaultKeyFileVaultUnlock{
				Password: new("test-password"),
			},
		},
		mdm.WithUUID(uuid),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestAutomaticCertificates(t *testing.T) {
	for _, kind := range []string{"personal", "institutional"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
			st := state.NewMemory()
			st.Now = func() time.Time { return now }
			manager := &replycerts.Manager{Store: st}
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
			original := rotation(t, kind, "rotation-1")
			const workers = 8
			outputs := make([]*mdm.Command, workers)
			errs := make([]error, workers)
			var wg sync.WaitGroup
			for i := range workers {
				wg.Go(func() { outputs[i], errs[i] = manager.Prepare(ctx, id, original) })
			}
			wg.Wait()
			for i := range workers {
				if errs[i] != nil {
					t.Fatal(errs[i])
				}
				if !bytes.Equal(outputs[0].Raw, outputs[i].Raw) {
					t.Fatal("concurrent preparation selected different identities")
				}
			}
			cert, _, err := manager.Recipient(ctx, id, original.UUID)
			if err != nil {
				t.Fatal(err)
			}
			public := requireType[*rsa.PublicKey](t, cert.PublicKey)
			if public.N.BitLen() < 2048 || cert.NotAfter.Sub(cert.NotBefore) < 364*24*time.Hour {
				t.Fatal("invalid generated certificate")
			}
			body := requireType[*commands.RotateFileVaultKey](t, outputs[0].Payload)
			der := body.ReplyEncryptionCertificate
			if kind == "institutional" {
				der = body.NewCertificate
			}
			if !bytes.Equal(der, cert.Raw) {
				t.Fatal("queued certificate differs from retained recipient")
			}
			if len(
				requireType[*commands.RotateFileVaultKey](t, original.Payload).ReplyEncryptionCertificate,
			) != 0 ||
				len(requireType[*commands.RotateFileVaultKey](t, original.Payload).NewCertificate) != 0 {
				t.Fatal("modified caller command")
			}
			// Exercise CMS with the generated recipient. The retained Go-generated
			// private key must still decrypt the reply after certificate expiry.
			plaintext := []byte("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF")
			envelope, err := pkcs7.Encrypt(plaintext, []*x509.Certificate{cert})
			if err != nil {
				t.Fatal(err)
			}
			now = now.AddDate(2, 0, 0)
			if n, err := st.Prune(ctx, 100); err != nil || n != 0 {
				t.Fatal("reply key expired", n, err)
			}
			restarted := &replycerts.Manager{Store: st}
			cert, key, err := restarted.Recipient(ctx, id, original.UUID)
			if err != nil {
				t.Fatal(err)
			}
			decrypted, err := cms.DecryptEnvelope(envelope, cert, key)
			if err != nil || !bytes.Equal(decrypted, plaintext) {
				t.Fatal("delayed response", err)
			}
			repeated, err := restarted.Prepare(ctx, id, outputs[0])
			if err != nil || !bytes.Equal(repeated.Raw, outputs[0].Raw) {
				t.Fatal("retry changed identity", err)
			}
			next, err := restarted.Prepare(ctx, id, rotation(t, kind, "rotation-2"))
			if err != nil || bytes.Equal(next.Raw, outputs[0].Raw) {
				t.Fatal("new rotation", err)
			}
			nextCert, _, err := restarted.Recipient(ctx, id, "rotation-2")
			if err != nil || public.Equal(nextCert.PublicKey) {
				t.Fatal("new command reused private key", err)
			}
			if _, _, err := restarted.Recipient(
				ctx,
				mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other"},
				original.UUID,
			); !errors.Is(
				err,
				state.ErrNotFound,
			) {
				t.Fatal("cross-enrollment lookup", err)
			}
			if err := restarted.Forget(ctx, id, original.UUID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := restarted.Recipient(
				ctx,
				id,
				original.UUID,
			); !errors.Is(
				err,
				state.ErrNotFound,
			) {
				t.Fatal("forgotten key remains", err)
			}
		})
	}
}

type failedStore struct{ state.Store }

var errUnavailable = errors.New("storage unavailable")

func (failedStore) Update(context.Context, []string, func(state.Tx) error) error {
	return errUnavailable
}

func TestCertificatePreparationRejectsConflictsAndStorageFailures(t *testing.T) {
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	st := state.NewMemory()
	manager := &replycerts.Manager{Store: st}
	cmd := rotation(t, "personal", "uuid")
	if _, err := (&replycerts.Manager{Store: failedStore{st}}).Prepare(
		ctx,
		id,
		cmd,
	); !errors.Is(
		err,
		errUnavailable,
	) {
		t.Fatal(err)
	}
	if records, err := st.List(ctx, "", "", 100); err != nil || len(records) != 0 {
		t.Fatal("failed persistence published identity", err)
	}
	prepared, err := manager.Prepare(ctx, id, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(
		ctx,
		id,
		rotation(t, "institutional", cmd.UUID),
	); !errors.Is(
		err,
		replycerts.ErrConflict,
	) {
		t.Fatal("different command accepted", err)
	}
	supplied := requireType[*commands.RotateFileVaultKey](t, prepared.Payload)
	manual, err := mdm.NewCommand(supplied, mdm.WithUUID("manual"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(ctx, id, manual); !errors.Is(err, replycerts.ErrConflict) {
		t.Fatal("external certificate accepted", err)
	}
	for _, badID := range []mdm.EnrollmentID{{Channel: mdm.ChannelUser, ID: "U"}, {Channel: mdm.ChannelDevice}} {
		if _, err := manager.Prepare(ctx, badID, cmd); !errors.Is(err, replycerts.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := manager.Prepare(ctx, id, nil); !errors.Is(err, replycerts.ErrInvalid) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := manager.Prepare(canceled, id, cmd); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEscrowProfileRetainsIdentity(t *testing.T) {
	ctx := t.Context()
	manager := &replycerts.Manager{Store: state.NewMemory()}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	first, err := manager.EscrowProfile(
		ctx,
		id,
		"escrow-command",
		"com.example.escrow",
		"Organization help desk",
	)
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.EscrowProfile(
		ctx,
		id,
		"escrow-command",
		"com.example.escrow",
		"Organization help desk",
	)
	if err != nil || !bytes.Equal(first.Raw, again.Raw) {
		t.Fatal("escrow retry changed profile or recipient", err)
	}
	parsed, err := profile.Parse(
		requireType[*commands.InstallProfile](t, first.Payload).Payload,
		profile.ParseOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	p := parsed.Profile
	if p.Scope != profile.ScopeSystem || len(p.Payloads) != 2 {
		t.Fatal("escrow profile structure")
	}
	certificate := requireType[*profiles.CertificatePKCS1](t, p.Payloads[0].Content)
	escrow := requireType[*profiles.FDERecoveryKeyEscrow](t, p.Payloads[1].Content)
	if escrow.EncryptCertPayloadUUID != p.Payloads[0].UUID {
		t.Fatal("escrow certificate reference")
	}
	cert, _, err := manager.Recipient(ctx, id, first.UUID)
	if err != nil || !bytes.Equal(certificate.PayloadContent, cert.Raw) {
		t.Fatal("escrow identity not retained", err)
	}
	if err := p.Validate(
		support.Target{
			OS:      support.MacOS,
			Version: support.V(26, 0, 0),
			Channel: support.ChannelDevice,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EscrowProfile(
		ctx,
		id,
		first.UUID,
		"com.example.different",
		"Organization help desk",
	); !errors.Is(
		err,
		replycerts.ErrConflict,
	) {
		t.Fatal("conflicting request accepted", err)
	}
	for _, args := range [][3]string{{"", "id", "location"}, {"id", "", "location"}, {"id", "profile", ""}} {
		if _, err := manager.EscrowProfile(
			ctx,
			id,
			args[0],
			args[1],
			args[2],
		); !errors.Is(
			err,
			replycerts.ErrInvalid,
		) {
			t.Fatal(err)
		}
	}
}
