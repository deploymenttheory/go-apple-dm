package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestCorruptMetadataDoesNotExposeSecretsOrDeliverCommands(t *testing.T) {
	for _, failure := range []string{"unlock export", "command metadata", "push metadata", "lock failure", "empty escrow", "invalid identity"} {
		t.Run(failure, func(t *testing.T) {
			ctx, now := t.Context(), time.Now()
			k := keyring(t, "storage-key-v1")
			s := openWith(t, filepath.Join(t.TempDir(), "metadata.db"), k)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			seedSecrets(t, s, id)
			switch failure {
			case "unlock export":
				sealed, err := k.Seal([]byte("secret"), crypt.AAD("wrong-purpose", "wrong-row"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB().ExecContext(ctx, "UPDATE enrollments SET unlock_token=? WHERE id=?", sealed, id.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Export(ctx, paging.Page{}); !errors.Is(err, crypt.ErrTampered) {
					t.Fatal("corrupt unlock exported", err)
				}
			case "command metadata":
				if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, &mdm.Command{UUID: "queued", RequestType: "DeviceInformation"}, storage.EnqueueOptions{}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB().ExecContext(ctx, "UPDATE commands SET enqueued_at='invalid'"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{}); err == nil {
					t.Fatal("corrupt command metadata accepted")
				}
			case "push metadata":
				if _, err := s.DB().ExecContext(ctx, "UPDATE enrollments SET channel='invalid' WHERE id=?", id.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PushInfo(ctx, []mdm.EnrollmentID{id}); err == nil {
					t.Fatal("corrupt push identity accepted")
				}
			case "lock failure":
				if _, err := s.DB().ExecContext(ctx, "CREATE TRIGGER refuse_lock BEFORE UPDATE OF id ON enrollments BEGIN SELECT RAISE(FAIL,'lock refused'); END"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: now}); err == nil {
					t.Fatal("unlocked replacement proceeded")
				}
				if err := s.StoreBootstrapToken(ctx, id, []byte("changed"), now); err == nil {
					t.Fatal("unlocked escrow write proceeded")
				}
			case "empty escrow":
				if _, err := s.DB().ExecContext(ctx, "UPDATE enrollments SET bootstrap_token=? WHERE id=?", []byte{}, id.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Rewrap(ctx); err != nil {
					t.Fatal("empty escrow broke rotation", err)
				}
			case "invalid identity":
				if _, err := s.CertHash(ctx, mdm.EnrollmentID{}); !errors.Is(err, storage.ErrInvalid) {
					t.Fatal(err)
				}
				if err := s.TouchLastSeen(ctx, mdm.EnrollmentID{}, now); !errors.Is(err, storage.ErrInvalid) {
					t.Fatal(err)
				}
			}
		})
	}
}
