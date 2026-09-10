package sqlite_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestRetainedRawSecretsEncryptedAndRotated(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "raw.db")
	s := openWith(t, path, keyring(t, "storage-key-v1"))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	now := time.Now()
	secret := []byte("sentinel-raw-enrollment-secret")
	if err := s.UpsertAuthenticate(ctx, id, nil, secret, now); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(
		ctx,
		id,
		mdm.Push{Topic: "topic", Magic: "magic", Token: []byte{1}},
		nil,
		secret,
		now,
	); err != nil {
		t.Fatal(err)
	}
	cmd := &mdm.Command{UUID: "command", RequestType: "InstallProfile", Raw: secret}
	if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := plist.Marshal(
		map[string]any{
			"UDID":        id.ID,
			"Status":      "Error",
			"CommandUUID": cmd.UUID,
			"ErrorChain": []map[string]any{
				{"ErrorCode": 1, "ErrorDomain": "test", "LocalizedDescription": string(secret)},
			},
		},
	)
	resp, _ := mdm.DecodeResponse(raw, "")
	if err := s.StoreResult(ctx, id, resp, now); err != nil {
		t.Fatal(err)
	}
	user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "device:alice", ParentID: id.ID}
	if err := s.StoreUserAuthChallenge(ctx, user, "nonce", secret, now); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreUserAuthToken(ctx, user, "token", secret, now); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"SELECT authenticate_raw FROM enrollments",
		"SELECT token_update_raw FROM enrollments",
		"SELECT raw FROM commands",
		"SELECT result_raw FROM commands",
		"SELECT result_error_chain FROM commands",
		"SELECT authenticate_raw FROM user_auth",
		"SELECT digest_raw FROM user_auth",
	}
	for _, query := range queries {
		var b []byte
		if err := s.DB().QueryRowContext(ctx, query).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if !crypt.IsSealed(b) || bytes.Contains(b, secret) {
			t.Fatal("plaintext", query)
		}
	}
	rotated := openWith(t, path, keyring(t, "storage-key-v2", "storage-key-v1"))
	if n, err := rotated.Rewrap(ctx); err != nil || n < 7 {
		t.Fatal(n, err)
	}
	active := openWith(t, path, keyring(t, "storage-key-v2"))
	e, err := active.Get(ctx, id)
	if err != nil || !bytes.Equal(e.TokenUpdateRaw, secret) {
		t.Fatal(err, e)
	}
	auth, err := active.UserAuth(ctx, user)
	if err != nil || !bytes.Equal(auth.DigestRaw, secret) {
		t.Fatal(err, auth)
	}
	// Swapping authenticated columns is detected even within the same row.
	if _, err := active.DB().
		ExecContext(ctx, "UPDATE enrollments SET authenticate_raw=token_update_raw"); err != nil {
		t.Fatal(err)
	}
	if _, err := active.Get(ctx, id); err == nil {
		t.Fatal("swapped ciphertext accepted")
	}
}

func TestAuthenticateAcrossIndependentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	left := openWith(t, path, nil)
	right := openWith(t, path, nil)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "contested"}
	results := make(chan error, 2)
	start := make(chan struct{})
	now := time.Now()
	var wg sync.WaitGroup
	for i, s := range []storage.Store{left, right} {
		wg.Go(func() {
			<-start
			results <- s.AuthenticateEnrollment(t.Context(), id, storage.AuthenticateChange{Hash: []string{"first", "second"}[i], At: now})
		})
	}
	close(start)
	wg.Wait()
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("exactly one pin must commit: %v, %v", a, b)
	}
	loser := a
	if loser == nil {
		loser = b
	}
	if !errors.Is(loser, storage.ErrConflict) {
		t.Fatal(loser)
	}
	enrolled, err := left.Get(t.Context(), id)
	if err != nil || enrolled.CertHash == "" {
		t.Fatal(enrolled, err)
	}
	if err = right.StoreTokenUpdate(
		t.Context(),
		id,
		mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"},
		&checkin.TokenUpdate{UnlockToken: []byte("escrow")},
		nil,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = left.DB().
		ExecContext(t.Context(), "CREATE TRIGGER deny_history BEFORE INSERT ON cert_associations BEGIN SELECT RAISE(FAIL,'history failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = right.AuthenticateEnrollment(
		t.Context(),
		id,
		storage.AuthenticateChange{ExpectedHash: enrolled.CertHash, Hash: "third", At: now},
	); err == nil {
		t.Fatal("failed transaction committed")
	}
	after, err := left.Get(t.Context(), id)
	if err != nil || after.CertHash != enrolled.CertHash || !after.Enabled ||
		string(after.UnlockToken) != "escrow" {
		t.Fatal("partial reset escaped transaction", after, err)
	}
}
