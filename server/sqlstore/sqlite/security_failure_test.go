package sqlite_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
)

func TestRetainedCiphertextCorruptionFailsClosed(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	for _, column := range []string{"authenticate_raw", "token_update_raw", "bootstrap_token", "command_raw", "result_raw", "result_error_chain", "user_authenticate_raw", "digest_raw"} {
		t.Run(column, func(t *testing.T) {
			k := keyring(t, "storage-key-v1")
			s := openWith(t, filepath.Join(t.TempDir(), "sealed.db"), k)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "device:u", ParentID: id.ID}
			seedSecrets(t, s, id)
			if _, err := s.Enqueue(
				ctx,
				[]mdm.EnrollmentID{id},
				&mdm.Command{
					UUID:        "command",
					RequestType: "DeviceInformation",
					Raw:         []byte("profile-secret"),
				},
				storage.EnqueueOptions{},
			); err != nil {
				t.Fatal(err)
			}
			swapped, err := k.Seal(
				[]byte("secret-from-another-purpose"),
				crypt.AAD("unrelated", "row"),
			)
			if err != nil {
				t.Fatal(err)
			}
			query := "UPDATE enrollments SET " + column + " = ? WHERE id='device'"
			reads := map[string]func() error{}
			switch column {
			case "authenticate_raw", "token_update_raw":
				reads["Get"] = func() error { _, err := s.Get(ctx, id); return err }
				reads["Resolve"] = func() error { _, err := s.EnrollmentByID(ctx, id.ID); return err }
				reads["List"] = func() error { _, err := s.List(ctx, storage.EnrollmentQuery{}, paging.Page{}); return err }
				reads["Export"] = func() error { _, err := s.Export(ctx, paging.Page{}); return err }
			case "bootstrap_token":
				reads["Bootstrap"] = func() error { _, err := s.BootstrapToken(ctx, id); return err }
				reads["Export"] = func() error { _, err := s.Export(ctx, paging.Page{}); return err }
			case "command_raw", "result_raw", "result_error_chain":
				col := column
				if col == "command_raw" {
					col = "raw"
				}
				query = "UPDATE commands SET " + col + " = ?"
				if column == "command_raw" {
					reads["Next"] = func() error { _, err := s.Next(ctx, id, false, now); return err }
				}
				reads["Commands"] = func() error { _, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{}); return err }
			default:
				col := column
				if col == "user_authenticate_raw" {
					col = "authenticate_raw"
				}
				query = "UPDATE user_auth SET " + col + " = ?"
				reads["UserAuth"] = func() error { _, err := s.UserAuth(ctx, uid); return err }
			}
			if _, err = s.DB().ExecContext(ctx, query, swapped); err != nil {
				t.Fatal(err)
			}
			for name, read := range reads {
				if err := read(); !errors.Is(err, crypt.ErrTampered) {
					t.Fatal(name, err)
				}
			}
		})
	}
}

func TestReplacementCommitFailuresRollBackPinAndTokens(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	for _, failure := range []string{"pin", "history", "child insert", "child update", "replacement write", "history lookup", "candidate reused", "child identity conflict"} {
		t.Run(failure, func(t *testing.T) {
			s := openWith(t, filepath.Join(t.TempDir(), "replace.db"), keyring(t, "storage-key-v1"))
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "new-user", ParentID: id.ID}
			seedSecrets(t, s, id)
			if err := s.AssociateCert(ctx, id, "old", now); err != nil {
				t.Fatal(err)
			}
			command, err := mdm.NewCommand(
				&commands.InstallProfile{Payload: []byte("profile")},
				mdm.WithUUID("replacement"),
			)
			if err != nil {
				t.Fatal(err)
			}
			steps := []storage.ReplacementChange{
				{
					Op: "begin",
					Begin: &storage.Replacement{
						ID:         "replacement",
						Method:     "acme",
						OldHash:    "old",
						SecretHash: "secret",
						ExpiresAt:  now.Add(30 * time.Minute),
						Command:    *command,
					},
				},
				{Op: "issue", Method: "acme", Hash: "new"},
				{Op: "deliver", Hash: "old"},
				{Op: "authenticate", Hash: "new", Raw: []byte("new-auth")},
				{
					Op:   "token",
					Hash: "new",
					Token: &storage.ReplacementToken{
						ID:  uid,
						Raw: []byte("child-raw"),
						Message: &checkin.TokenUpdate{
							Topic:     "topic",
							PushMagic: "new",
							Token:     []byte{1},
						},
					},
				},
				{
					Op:   "token",
					Hash: "new",
					Token: &storage.ReplacementToken{
						ID:  id,
						Raw: []byte("device-raw"),
						Message: &checkin.TokenUpdate{
							Topic:     "topic",
							PushMagic: "new",
							Token:     []byte{1},
						},
					},
				},
			}
			for _, step := range steps {
				step.At = now
				step.ID = "replacement"
				if _, err := s.TransitionReplacement(ctx, id, step); err != nil {
					t.Fatal(step.Op, err)
				}
			}
			var before []byte
			if err := s.DB().
				QueryRowContext(ctx, "SELECT state_blob FROM enrollment_replacements").
				Scan(&before); err != nil {
				t.Fatal(err)
			}
			query := ""
			switch failure {
			case "pin":
				query = "CREATE TRIGGER fault BEFORE UPDATE OF cert_hash ON enrollments BEGIN SELECT RAISE(FAIL,'pin denied'); END"
			case "history":
				query = "CREATE TRIGGER fault BEFORE INSERT ON cert_associations BEGIN SELECT RAISE(FAIL,'history denied'); END"
			case "child insert":
				query = "CREATE TRIGGER fault BEFORE INSERT ON enrollments WHEN NEW.parent_id <> '' BEGIN SELECT RAISE(FAIL,'child denied'); END"
			case "child update":
				query = "CREATE TRIGGER fault BEFORE UPDATE OF token_update_raw ON enrollments WHEN NEW.parent_id <> '' BEGIN SELECT RAISE(FAIL,'child update denied'); END"
			case "replacement write":
				query = "CREATE TRIGGER fault BEFORE UPDATE ON enrollment_replacements BEGIN SELECT RAISE(FAIL,'replacement denied'); END"
			case "history lookup":
				query = "DROP TABLE cert_associations"
			case "child identity conflict":
				if err := s.UpsertAuthenticate(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: uid.ID}, nil, nil, now); err != nil {
					t.Fatal(err)
				}
			case "candidate reused":
				other := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other"}
				if err := s.UpsertAuthenticate(ctx, other, nil, nil, now); err != nil {
					t.Fatal(err)
				}
				if err := s.AssociateCert(ctx, other, "new", now); err != nil {
					t.Fatal(err)
				}
			}
			if query != "" {
				if _, err := s.DB().ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.TransitionReplacement(
				ctx,
				id,
				storage.ReplacementChange{
					Op:   "result",
					ID:   "replacement",
					Hash: "new",
					At:   now,
					Response: &mdm.Response{
						CommandUUID: command.UUID,
						Status:      mdm.StatusAcknowledged,
					},
				},
			)
			if err == nil {
				t.Fatal("failed commit reported success")
			}
			after, err := s.Get(ctx, id)
			if err != nil || after.CertHash != "old" || after.Push.Magic != "m" {
				t.Fatal("partial replacement escaped", after, err)
			}
			if _, err = s.Get(ctx, uid); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("partial child creation escaped", err)
			}
			var current []byte
			if err = s.DB().
				QueryRowContext(ctx, "SELECT state_blob FROM enrollment_replacements").
				Scan(&current); err != nil ||
				!bytes.Equal(before, current) {
				t.Fatal("failed transition changed replacement state", err)
			}
		})
	}
}

func TestAtomicAuthenticateRejectsReuseAndRollsBackStorageFailure(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	for _, failure := range []string{"initialize", "history lookup", "reset", "pin", "commit", "reuse"} {
		t.Run(failure, func(t *testing.T) {
			s := openWith(t, filepath.Join(t.TempDir(), "authenticate.db"), nil)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "new-device"}
			query := ""
			switch failure {
			case "initialize":
				query = "CREATE TRIGGER fault BEFORE INSERT ON enrollments BEGIN SELECT RAISE(FAIL,'create denied'); END"
			case "history lookup":
				query = "DROP TABLE cert_associations"
			case "reset":
				query = "CREATE TRIGGER fault BEFORE UPDATE OF device_name ON enrollments BEGIN SELECT RAISE(FAIL,'reset denied'); END"
			case "pin":
				query = "CREATE TRIGGER fault BEFORE UPDATE OF cert_hash ON enrollments WHEN NEW.cert_hash <> '' BEGIN SELECT RAISE(FAIL,'pin denied'); END"
			case "commit":
				query = "CREATE TABLE invalid_commit(owner TEXT REFERENCES enrollments(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_commit AFTER UPDATE OF cert_hash ON enrollments WHEN NEW.cert_hash <> '' BEGIN INSERT INTO invalid_commit VALUES ('absent'); END"
			case "reuse":
				other := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other"}
				if err := s.AuthenticateEnrollment(
					ctx,
					other,
					storage.AuthenticateChange{Hash: "used", At: now},
				); err != nil {
					t.Fatal(err)
				}
			}
			if query != "" {
				if _, err := s.DB().ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.AuthenticateEnrollment(
				ctx,
				id,
				storage.AuthenticateChange{Hash: "used", At: now},
			); err == nil {
				t.Fatal("authentication failure ignored")
			}
			if _, err := s.EnrollmentByID(ctx, id.ID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("partial enrollment survived rollback", err)
			}
		})
	}
	s := openWith(t, filepath.Join(t.TempDir(), "anonymous.db"), nil)
	if err := s.AuthenticateEnrollment(
		ctx,
		mdm.EnrollmentID{},
		storage.AuthenticateChange{At: now},
	); !errors.Is(
		err,
		storage.ErrInvalid,
	) {
		t.Fatal(err)
	}
	if err := s.AuthenticateEnrollment(
		ctx,
		mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "development"},
		storage.AuthenticateChange{At: now},
	); err != nil {
		t.Fatal("explicit unpinned development transition failed", err)
	}
}

func TestCorruptCapabilityAndReplacementRecordsAreRejected(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	s := openWith(t, filepath.Join(t.TempDir(), "corrupt.db"), nil)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	seedSecrets(t, s, id)
	if _, err := s.DB().
		ExecContext(ctx, "UPDATE enrollments SET capabilities = ? WHERE id = ?", []byte("invalid JSON"), id.ID); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() error{
		"get":    func() error { _, err := s.Get(ctx, id); return err },
		"list":   func() error { _, err := s.List(ctx, storage.EnrollmentQuery{}, paging.Page{}); return err },
		"export": func() error { _, err := s.Export(ctx, paging.Page{}); return err },
		"replacement": func() error {
			_, err := s.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: now})
			return err
		},
	} {
		if err := read(); err == nil {
			t.Fatal(name, "accepted corrupt capability record")
		}
	}
	if _, err := s.DB().
		ExecContext(ctx, "UPDATE enrollments SET capabilities = NULL WHERE id = ?", id.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().
		ExecContext(ctx, "INSERT INTO enrollment_replacements(enrollment_id,state_blob) VALUES (?,?)", id.ID, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{Op: "read", At: now},
	); err == nil {
		t.Fatal("corrupt replacement accepted")
	}
	sealed, err := keyring(t, "storage-key-v1").Seal([]byte("state"), crypt.AAD("unrelated", "row"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().
		ExecContext(ctx, "UPDATE enrollment_replacements SET state_blob=?", sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{Op: "read", At: now},
	); !errors.Is(
		err,
		crypt.ErrNoKeyring,
	) {
		t.Fatal(err)
	}
	if _, err = s.DB().ExecContext(ctx, "DROP TABLE enrollment_replacements"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionReplacement(
		ctx,
		id,
		storage.ReplacementChange{Op: "read", At: now},
	); err == nil {
		t.Fatal("missing replacement table ignored")
	}
	for _, invalid := range []mdm.EnrollmentID{{}, {Channel: mdm.ChannelUser, ID: "user", ParentID: id.ID}} {
		if _, err = s.TransitionReplacement(
			ctx,
			invalid,
			storage.ReplacementChange{Op: "read", At: now},
		); !errors.Is(
			err,
			storage.ErrInvalid,
		) {
			t.Fatal(err)
		}
	}
}

func TestUserAuthLifecycleAndQueueDisableFailures(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	s := openWith(t, filepath.Join(t.TempDir(), "lifecycle.db"), nil)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "parent"}
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "parent:child", ParentID: id.ID}
	seedSecrets(t, s, id)
	if err := s.UpsertAuthenticate(ctx, uid, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreUserAuthToken(
		ctx,
		uid,
		"token",
		nil,
		now,
	); !errors.Is(
		err,
		storage.ErrNotFound,
	) {
		t.Fatal("token accepted without challenge", err)
	}
	if _, err := s.UserAuth(ctx, mdm.EnrollmentID{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.UserAuth(
		ctx,
		mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "orphan", ParentID: "absent"},
	); !errors.Is(
		err,
		storage.ErrNotFound,
	) {
		t.Fatal(err)
	}
	if err := s.StoreUserAuthChallenge(ctx, uid, "challenge", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().
		ExecContext(ctx, "UPDATE user_auth SET challenge_at='invalid' WHERE enrollment_id=?", uid.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserAuth(ctx, uid); err == nil {
		t.Fatal("corrupt authentication timestamp accepted")
	}
	if _, err := s.DB().
		ExecContext(ctx, "CREATE TRIGGER fault BEFORE UPDATE ON commands BEGIN SELECT RAISE(FAIL,'queue failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(
		ctx,
		[]mdm.EnrollmentID{id},
		&mdm.Command{UUID: "queued", RequestType: "DeviceInformation"},
		storage.EnqueueOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if err := s.Disable(ctx, id, now); err == nil {
		t.Fatal("failed queue cancellation ignored")
	}
	e, err := s.Get(ctx, id)
	if err != nil || !e.Enabled {
		t.Fatal("failed disable committed partially", e, err)
	}
	if _, err = s.DB().ExecContext(ctx, "DROP TRIGGER fault"); err != nil {
		t.Fatal(err)
	}
	if err = s.Disable(ctx, id, now); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertAuthenticate(ctx, uid, nil, nil, now); !errors.Is(err, storage.ErrDisabled) {
		t.Fatal("child reset under disabled parent accepted", err)
	}
	if _, err = s.UserAuth(ctx, uid); !errors.Is(err, storage.ErrDisabled) {
		t.Fatal("disabled parent exposed user authentication", err)
	}
}

func TestUserAuthRejectsCorruptIdentityAndDisabledChild(t *testing.T) {
	for _, fault := range []string{"wrong parent", "wrong channel", "malformed channel", "missing table", "disabled child", "corrupt enrollment", "malformed enrollment channel"} {
		t.Run(fault, func(t *testing.T) {
			ctx, now := t.Context(), time.Now()
			s := openWith(
				t,
				filepath.Join(t.TempDir(), "identity.db"),
				keyring(t, "storage-key-v1"),
			)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "parent"}
			uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user", ParentID: id.ID}
			seedSecrets(t, s, id)
			if err := s.StoreUserAuthChallenge(
				ctx,
				uid,
				"challenge",
				[]byte("original"),
				now,
			); err != nil {
				t.Fatal(err)
			}
			var query string
			switch fault {
			case "wrong parent":
				other := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "another"}
				if err := s.UpsertAuthenticate(ctx, other, nil, nil, now); err != nil {
					t.Fatal(err)
				}
				query = "UPDATE user_auth SET parent_id='another' WHERE enrollment_id='user'"
			case "wrong channel":
				query = "UPDATE user_auth SET channel=99 WHERE enrollment_id='user'"
			case "malformed channel":
				query = "UPDATE user_auth SET channel='invalid' WHERE enrollment_id='user'"
			case "missing table":
				query = "DROP TABLE user_auth"
			case "disabled child":
				if err := s.Disable(ctx, uid, now); err != nil {
					t.Fatal(err)
				}
			case "corrupt enrollment":
				query = "UPDATE enrollments SET capabilities='invalid' WHERE id='user'"
			case "malformed enrollment channel":
				query = "UPDATE enrollments SET channel='invalid' WHERE id='user'"
			}
			if query != "" {
				if _, err := s.DB().ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			for name, call := range map[string]func() error{
				"read":      func() error { _, err := s.UserAuth(ctx, uid); return err },
				"clear":     func() error { return s.ClearUserAuth(ctx, uid) },
				"challenge": func() error { return s.StoreUserAuthChallenge(ctx, uid, "replacement", []byte("changed"), now) },
				"token":     func() error { return s.StoreUserAuthToken(ctx, uid, "token", []byte("changed"), now) },
			} {
				if err := call(); err == nil {
					t.Fatal(name, "accepted invalid user identity")
				}
			}
			if fault != "missing table" && fault != "disabled child" {
				var challenge string
				if err := s.DB().
					QueryRowContext(ctx, "SELECT challenge FROM user_auth WHERE enrollment_id='user'").
					Scan(&challenge); err != nil ||
					challenge != "challenge" {
					t.Fatal("rejected operation mutated challenge", challenge, err)
				}
			}
		})
	}
}

func TestCapabilityPersistenceFailureRollsBackAcknowledgment(t *testing.T) {
	ctx, now := t.Context(), time.Now()
	s := openWith(t, filepath.Join(t.TempDir(), "capabilities.db"), keyring(t, "storage-key-v1"))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	seedSecrets(t, s, id)
	command := &mdm.Command{UUID: "observation", RequestType: "DeviceInformation"}
	if _, err := s.Enqueue(
		ctx,
		[]mdm.EnrollmentID{id},
		command,
		storage.EnqueueOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().
		ExecContext(ctx, "CREATE TRIGGER deny_capabilities BEFORE UPDATE OF capabilities ON enrollments BEGIN SELECT RAISE(FAIL,'capability update failed'); END"); err != nil {
		t.Fatal(err)
	}
	raw, err := plist.Marshal(
		map[string]any{
			"UDID":           id.ID,
			"CommandUUID":    command.UUID,
			"Status":         "Acknowledged",
			"QueryResponses": map[string]any{"IsSupervised": true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := mdm.DecodeResponse(raw, "DeviceInformation")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StoreResult(ctx, id, response, now); err == nil {
		t.Fatal("failed capability update reported success")
	}
	enrollment, err := s.Get(ctx, id)
	if err != nil || enrollment.Capabilities.Supervised != storage.CapabilityUnknown {
		t.Fatal("failed observation granted eligibility", enrollment, err)
	}
	queued, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{})
	if err != nil || len(queued.Items) != 1 || queued.Items[0].State != storage.StatePending {
		t.Fatal("failed observation committed acknowledgment", queued, err)
	}
	if _, err = s.DB().ExecContext(ctx, "DROP TRIGGER deny_capabilities; CREATE TRIGGER corrupt_identity AFTER UPDATE ON commands BEGIN UPDATE enrollments SET capabilities='invalid' WHERE id='device'; END"); err != nil {
		t.Fatal(err)
	}
	if err = s.StoreResult(ctx, id, response, now); err == nil {
		t.Fatal("corrupt identity committed acknowledgment")
	}
	if _, err = s.Get(ctx, id); err != nil {
		t.Fatal("corruption escaped rollback", err)
	}
	if err = s.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnrollmentByID(ctx, id.ID); err == nil {
		t.Fatal("unavailable identity lookup reported success")
	}
}
