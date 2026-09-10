package storagetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// RunReplacementSuite verifies the same atomic transition contract on every backend.
func RunReplacementSuite(t *testing.T, factory Factory) {
	ctx := context.Background()
	for _, method := range []string{"scep", "acme"} {
		for _, terminal := range []string{"commit-token-first", "commit-ack-first", "cancel", "error", "expire"} {
			t.Run(method+"/"+terminal, func(t *testing.T) {
				s := factory(t)
				rs, ok := s.(storage.ReplacementStore)
				if !ok {
					t.Skip("optional replacement store unavailable")
				}
				id := device(1)
				enroll(t, s, id, 1)
				enroll(t, s, user(1, "alice"), 2)
				if err := s.AssociateCert(ctx, id, "old", t0); err != nil {
					t.Fatal(err)
				}
				if err := s.StoreBootstrapToken(ctx, id, []byte("escrow"), t0); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd(t, "queued"), storage.EnqueueOptions{Now: t0}); err != nil {
					t.Fatal(err)
				}
				command, _ := mdm.NewCommand(&commands.InstallProfile{Payload: []byte("profile")}, mdm.WithUUID("attempt"))
				x := &storage.Replacement{ID: "attempt", Method: method, OldHash: "old", SecretHash: "secret", ExpiresAt: t0.Add(30 * time.Minute), Command: *command}
				step := func(c storage.ReplacementChange) *storage.Replacement {
					t.Helper()
					if c.At.IsZero() {
						c.At = t0
					}
					if c.ID == "" {
						c.ID = "attempt"
					}
					r, err := rs.TransitionReplacement(ctx, id, c)
					if err != nil {
						t.Fatal(c.Op, err)
					}
					return r
				}
				step(storage.ReplacementChange{Op: "begin", Begin: x})
				if _, err := rs.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "begin", Begin: x, At: t0}); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("concurrent attempt", err)
				}
				if method == "scep" {
					step(storage.ReplacementChange{Op: "claim", SecretHash: "secret", PublicKeyHash: "key"})
				}
				step(storage.ReplacementChange{Op: "issue", Method: method, Hash: "new", PublicKeyHash: "key"})
				step(storage.ReplacementChange{Op: "deliver", Hash: "old"})
				step(storage.ReplacementChange{Op: "authenticate", Hash: "new", Raw: []byte("new-auth")})
				before, _ := s.Get(ctx, id)
				if before.CertHash != "old" || !before.Enabled {
					t.Fatal("Authenticate changed active enrollment")
				}
				token := func(id mdm.EnrollmentID) storage.ReplacementChange {
					return storage.ReplacementChange{Op: "token", Hash: "new", Token: &storage.ReplacementToken{ID: id, Raw: []byte("new-token"), Message: &checkin.TokenUpdate{Topic: "com.apple.mgmt.test", Token: []byte{9, 8}, PushMagic: "new-magic", UserShortName: new("updated"), UserLongName: "Updated", EnrollmentUserID: "enrollment-user", UnlockToken: []byte("new-unlock")}}}
				}
				// Include existing and newly created user channels in the transaction.
				step(token(user(1, "alice")))
				step(token(user(1, "bob")))
				step(token(user(1, "alice")))
				ack := storage.ReplacementChange{Op: "result", Hash: "new", Response: &mdm.Response{CommandUUID: "attempt", Status: mdm.StatusAcknowledged}}
				var final *storage.Replacement
				switch terminal {
				case "commit-token-first":
					step(token(id))
					final = step(ack)
				case "commit-ack-first":
					step(ack)
					final = step(token(id))
				case "cancel":
					step(token(id))
					final = step(storage.ReplacementChange{Op: "cancel"})
				case "error":
					step(token(id))
					ack.Response.Status = mdm.StatusError
					final = step(ack)
				case "expire":
					step(token(id))
					final = step(storage.ReplacementChange{Op: "read", At: t0.Add(30 * time.Minute)})
				}
				current, _ := s.Get(ctx, id)
				if terminal == "commit-token-first" || terminal == "commit-ack-first" {
					if final.State != storage.ReplacementCommitted || current.CertHash != "new" || current.Push.Magic != "new-magic" || string(current.AuthenticateRaw) != "new-auth" {
						t.Fatal("candidate not committed", final.State, current)
					}
					for _, uid := range []mdm.EnrollmentID{user(1, "alice"), user(1, "bob")} {
						u, err := s.Get(ctx, uid)
						if err != nil || u.Push.Magic != "new-magic" {
							t.Fatal("user token not committed", u, err)
						}
					}
					history, _ := s.CertHistory(ctx, id)
					if len(history) != 2 {
						t.Fatal("pin history", history)
					}
				} else if current.CertHash != "old" || current.Push.Magic != before.Push.Magic {
					t.Fatal("failed update damaged working enrollment", current)
				}
				if !current.Enabled || !current.EnrolledAt.Equal(before.EnrolledAt) {
					t.Fatal("enrollment was reset")
				}
				escrow, err := s.BootstrapToken(ctx, id)
				if err != nil || string(escrow) != "escrow" {
					t.Fatal("escrow lost", err)
				}
				next, err := s.Next(ctx, id, false, t0)
				if err != nil || next == nil || next.UUID != "queued" {
					t.Fatal("queue lost", next, err)
				}
				// Returned values cannot mutate the durable attempt.
				final.State = "corrupt"
				final.Command.Raw[0] = '!'
				saved := step(storage.ReplacementChange{Op: "read"})
				if saved.State == "corrupt" || saved.Command.Raw[0] == '!' {
					t.Fatal("returned state aliases storage")
				}
			})
		}
	}
	t.Run("ConcurrentBegin", func(t *testing.T) {
		s := factory(t)
		rs, ok := s.(storage.ReplacementStore)
		if !ok {
			t.Skip("optional replacement store unavailable")
		}
		id := device(1)
		enroll(t, s, id, 1)
		_ = s.AssociateCert(ctx, id, "old", t0)
		command, _ := mdm.NewCommand(&commands.InstallProfile{Payload: []byte("p")}, mdm.WithUUID("attempt"))
		ch := storage.ReplacementChange{Op: "begin", At: t0, Begin: &storage.Replacement{ID: "attempt", Method: "acme", OldHash: "old", ExpiresAt: t0.Add(time.Minute), Command: *command}}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Go(func() { _, err := rs.TransitionReplacement(ctx, id, ch); results <- err })
		}
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if !errors.Is(err, storage.ErrConflict) {
				t.Fatal(err)
			}
		}
		if wins != 1 {
			t.Fatal("concurrent winners", wins)
		}
	})
}
