package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

func TestReplacementProfileClaimsItsPendingAttempt(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			a, err := Build(t.Context(), Config{
				Role:    RoleAll,
				Storage: "inmem",
				DSN:     filepath.Join(t.TempDir(), "mdm.sqlite"),
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
				Enroll: EnrollConfig{
					PublicURL: "https://mdm.example",
					Topic:     "com.apple.mgmt.test",
					Admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
						return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if backend == "sqlite" {
				st, err := sqlite.Open(
					t.Context(),
					filepath.Join(t.TempDir(), "mdm.sqlite"),
					sqlite.Options{},
				)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
				a.Store = st
				protocol, err := statestore.Open(t.Context(), st.DB(), sqlite.Dialect)
				if err != nil {
					t.Fatal(err)
				}
				a.protocol, a.enroll.state = protocol, protocol
			}
			id := mdm.EnrollmentID{
				Channel: mdm.ChannelDevice,
				ID:      "11111111-2222-3333-4444-555555555555",
			}
			err = a.Store.Import(
				t.Context(),
				storage.EnrollmentExport{
					Enrollment: storage.Enrollment{
						ID:         id,
						Enabled:    true,
						CertHash:   "original",
						EnrolledAt: time.Now(),
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.ExportEnrollmentProfile(
				t.Context(),
				EnrollmentProfileRequest{
					DeviceID:     id.ID,
					Product:      "Mac",
					Identity:     "scep",
					AccessRights: enroll.RightInstallProfiles,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			x, err := a.prepareReplacement(t.Context(), id, "scep")
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.replacementStore().
				TransitionReplacement(t.Context(), id, storage.ReplacementChange{Op: "begin", Begin: x, At: a.cfg.Clock.Now()})
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := mdm.DecodeCommand(x.Command.Raw)
			if err != nil {
				t.Fatal(err)
			}
			p, err := enroll.Parse(
				cmd.Payload.(*commands.InstallProfile).Payload,
				profile.ParseOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.SCEP.Subject.CommonName) > 64 {
				t.Fatal("replacement subject exceeds the macOS installer limit")
			}
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := x509.CreateCertificateRequest(
				rand.Reader,
				&x509.CertificateRequest{Subject: p.SCEP.Subject},
				key,
			)
			if err != nil {
				t.Fatal(err)
			}
			csr, err := x509.ParseCertificateRequest(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.replacementChallenge(t.Context(), p.SCEP.Challenge, csr); err != nil {
				t.Fatal(err)
			}
			// A second app resolves the binding from persistent state, including
			// after the attempt's issuance deadline; installed identities outlive it.
			reader := a.protocol
			if st, ok := a.Store.(*sqlite.Store); ok {
				reader, err = statestore.Open(t.Context(), st.DB(), sqlite.Dialect)
				if err != nil {
					t.Fatal(err)
				}
			}
			restarted := &App{protocol: reader}
			got, attempt, err := restarted.resolveReplacementSubject(
				t.Context(),
				p.SCEP.Subject.CommonName,
			)
			if err != nil || got != id || attempt != x.ID {
				t.Fatal(got, attempt, err)
			}
			if err := a.replacementChallenge(t.Context(), "wrong", csr); err == nil {
				t.Fatal("subject reference bypassed challenge authorization")
			}
			if err := a.bindReplacementSubject(t.Context(), id, x.ID); err != nil {
				t.Fatal(err)
			}
			other := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other"}
			if err := a.bindReplacementSubject(
				t.Context(),
				other,
				x.ID,
			); !errors.Is(
				err,
				storage.ErrConflict,
			) {
				t.Fatal("subject reference rebound to another device", err)
			}
			stored, err := a.Store.Get(t.Context(), id)
			if err != nil || stored.CertHash != "original" {
				t.Fatal("claim replaced pin", err)
			}
		})
	}
}

func TestReplacementSubjectLookupFailsClosed(t *testing.T) {
	a, id := replacementSecurityApp(t)
	attempt := profile.NewUUID()
	subject := replacementSubjectPrefix + attempt
	for _, invalid := range []string{"", replacementSubjectPrefix, replacementSubjectPrefix + "01A0", "other:" + attempt, subject} {
		if _, _, err := a.resolveReplacementSubject(t.Context(), invalid); err == nil {
			t.Fatal("unbound subject accepted", invalid)
		}
	}
	legacy := replacementSubject(id, "legacy-attempt")
	got, ref, err := a.resolveReplacementSubject(t.Context(), legacy)
	if err != nil || got != id || ref != "legacy-attempt" {
		t.Fatal(got, ref, err)
	}
	key := "replacement-subject:" + attempt
	if err := a.protocol.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: key})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.resolveReplacementSubject(
		t.Context(),
		subject,
	); !errors.Is(
		err,
		storage.ErrInvalid,
	) {
		t.Fatal("corrupt binding accepted", err)
	}
	backing := a.protocol
	failure := errors.New("protocol state unavailable")
	a.protocol = issuanceStateFault{Store: backing, readErr: failure, txReadErr: failure}
	if _, _, err := a.resolveReplacementSubject(t.Context(), subject); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := a.bindReplacementSubject(
		t.Context(),
		id,
		profile.NewUUID(),
	); !errors.Is(
		err,
		failure,
	) {
		t.Fatal(err)
	}
	a.protocol = nil
	if _, _, err := a.resolveReplacementSubject(
		t.Context(),
		subject,
	); !errors.Is(
		err,
		storage.ErrInvalid,
	) {
		t.Fatal(err)
	}
}
