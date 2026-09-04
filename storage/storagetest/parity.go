package storagetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

const testTopic = "com.apple.mgmt.test"

// pushPair issues a push certificate for topic valid from t0 minus an hour.
func pushPair(t *testing.T, ca *testpki.CA, topic string) (certPEM, keyPEM []byte) {
	t.Helper()
	id, err := ca.IssuePush(topic, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err = id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM
}

// RunPushCertSuite covers PushCertStore (decision record 0015).
func RunPushCertSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	ca, err := testpki.NewCA("storagetest push")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := pushPair(t, ca, testTopic)

	t.Run("StoreGetList", func(t *testing.T) {
		s := newStore(t)
		rec, err := s.StorePushCert(ctx, "", certPEM, keyPEM, t0)
		if err != nil || rec.Topic != testTopic || rec.Version != 1 || len(rec.KeyPEM) != 0 || rec.NotAfter.IsZero() || !rec.UpdatedAt.Equal(t0) {
			t.Fatalf("StorePushCert = %+v %v", rec, err)
		}
		got, err := s.PushCert(ctx, testTopic)
		if err != nil || !bytes.Equal(got.CertPEM, certPEM) || !bytes.Equal(got.KeyPEM, keyPEM) || got.Version != 1 || !got.NotAfter.Equal(rec.NotAfter) {
			t.Fatalf("PushCert = %+v %v", got, err)
		}
		// Returned copies do not alias store state.
		got.KeyPEM[0] = 'X'
		if again, _ := s.PushCert(ctx, testTopic); again.KeyPEM[0] == 'X' {
			t.Fatal("PushCert returned an aliased key")
		}
		other, otherKey := pushPair(t, ca, "com.apple.mgmt.aaa")
		if _, err := s.StorePushCert(ctx, "com.apple.mgmt.aaa", other, otherKey, t0); err != nil {
			t.Fatal(err)
		}
		list, err := s.PushCerts(ctx)
		if err != nil || len(list) != 2 || list[0].Topic != "com.apple.mgmt.aaa" || list[1].Topic != testTopic || len(list[0].KeyPEM) != 0 || len(list[1].KeyPEM) != 0 {
			t.Fatalf("PushCerts = %+v %v", list, err)
		}
		if v, err := s.PushCertVersion(ctx, testTopic); err != nil || v != 1 {
			t.Fatalf("PushCertVersion = %d %v", v, err)
		}
	})

	t.Run("OverwriteBumpsVersion", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.StorePushCert(ctx, testTopic, certPEM, keyPEM, t0); err != nil {
			t.Fatal(err)
		}
		renewed, renewedKey := pushPair(t, ca, testTopic)
		rec, err := s.StorePushCert(ctx, testTopic, renewed, renewedKey, t0.Add(time.Minute))
		if err != nil || rec.Version != 2 || !rec.UpdatedAt.Equal(t0.Add(time.Minute)) {
			t.Fatalf("renewal = %+v %v", rec, err)
		}
		if v, _ := s.PushCertVersion(ctx, testTopic); v != 2 {
			t.Fatalf("version after renewal = %d", v)
		}
		if got, _ := s.PushCert(ctx, testTopic); !bytes.Equal(got.CertPEM, renewed) || !bytes.Equal(got.KeyPEM, renewedKey) {
			t.Fatal("renewal did not replace the stored pair")
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		s := newStore(t)
		_, otherKey := pushPair(t, ca, "com.apple.mgmt.other")
		noTopic, err := ca.Issue("no-topic", t0.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		noTopicCert, noTopicKey, _ := noTopic.PEM()
		cases := map[string]struct {
			topic     string
			cert, key []byte
			at        time.Time
		}{
			"garbage PEM":     {"", []byte("nope"), keyPEM, t0},
			"empty":           {"", nil, nil, t0},
			"key mismatch":    {"", certPEM, otherKey, t0},
			"topic mismatch":  {"com.apple.mgmt.other", certPEM, keyPEM, t0},
			"no topic in UID": {"", noTopicCert, noTopicKey, t0},
			"expired":         {"", certPEM, keyPEM, t0.Add(10 * 365 * 24 * time.Hour)},
			"not yet valid":   {"", certPEM, keyPEM, t0.Add(-48 * time.Hour)},
		}
		for name, c := range cases {
			if _, err := s.StorePushCert(ctx, c.topic, c.cert, c.key, c.at); !errors.Is(err, storage.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if list, err := s.PushCerts(ctx); err != nil || len(list) != 0 {
			t.Fatalf("rejected certificates were stored: %+v %v", list, err)
		}
		if _, err := s.PushCert(ctx, testTopic); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown topic: %v", err)
		}
		if _, err := s.PushCertVersion(ctx, testTopic); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown topic version: %v", err)
		}
	})
}

// RunUserAuthSuite covers UserAuthStore (decision record 0016).
func RunUserAuthSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	uid := user(1, "alice")

	t.Run("DeviceChannelInvalid", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		dev := device(1)
		calls := map[string]func() error{
			"challenge": func() error { return s.StoreUserAuthChallenge(ctx, dev, "c", nil, t0) },
			"token":     func() error { return s.StoreUserAuthToken(ctx, dev, "t", nil, t0) },
			"get":       func() error { _, err := s.UserAuth(ctx, dev); return err },
			"clear":     func() error { return s.ClearUserAuth(ctx, dev) },
			"bad id":    func() error { return s.StoreUserAuthChallenge(ctx, mdm.EnrollmentID{}, "c", nil, t0) },
			"empty challenge": func() error {
				return s.StoreUserAuthChallenge(ctx, uid, "", nil, t0)
			},
			"empty token": func() error { return s.StoreUserAuthToken(ctx, uid, "", nil, t0) },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, storage.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("ParentMissing", func(t *testing.T) {
		s := newStore(t)
		calls := map[string]func() error{
			"challenge": func() error { return s.StoreUserAuthChallenge(ctx, uid, "c", nil, t0) },
			"token":     func() error { return s.StoreUserAuthToken(ctx, uid, "t", nil, t0) },
			"get":       func() error { _, err := s.UserAuth(ctx, uid); return err },
			"clear":     func() error { return s.ClearUserAuth(ctx, uid) },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, storage.ErrNotFound) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("ChallengeAndTokenRoundTrip", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		// The user's own enrollment row does not exist yet: the handshake
		// precedes the user channel's TokenUpdate.
		if _, err := s.UserAuth(ctx, uid); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("before challenge: %v", err)
		}
		if err := s.StoreUserAuthToken(ctx, uid, "tok", nil, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("token without challenge: %v", err)
		}
		if err := s.StoreUserAuthChallenge(ctx, uid, "nonce-1", []byte("<ua1/>"), t0); err != nil {
			t.Fatal(err)
		}
		st, err := s.UserAuth(ctx, uid)
		if err != nil || st.ID != uid || st.Challenge != "nonce-1" || !st.ChallengeAt.Equal(t0) || st.AuthToken != "" || string(st.AuthenticateRaw) != "<ua1/>" {
			t.Fatalf("after challenge: %+v %v", st, err)
		}
		if err := s.StoreUserAuthToken(ctx, uid, "tok-1", []byte("<ua2/>"), t0.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		st, _ = s.UserAuth(ctx, uid)
		if st.Challenge != "" || !st.ChallengeAt.IsZero() || st.AuthToken != "tok-1" || !st.TokenAt.Equal(t0.Add(time.Second)) || string(st.DigestRaw) != "<ua2/>" || string(st.AuthenticateRaw) != "<ua1/>" {
			t.Fatalf("after token: %+v", st)
		}
		// Copies do not alias.
		st.DigestRaw[1] = 'X'
		if again, _ := s.UserAuth(ctx, uid); string(again.DigestRaw) != "<ua2/>" {
			t.Fatal("aliased raw plist")
		}
		// A new challenge replaces the token.
		if err := s.StoreUserAuthChallenge(ctx, uid, "nonce-2", nil, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		st, _ = s.UserAuth(ctx, uid)
		if st.Challenge != "nonce-2" || st.AuthToken != "" || !st.TokenAt.IsZero() {
			t.Fatalf("second challenge: %+v", st)
		}
		// Clearing is idempotent.
		if err := s.ClearUserAuth(ctx, uid); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearUserAuth(ctx, uid); err != nil {
			t.Fatalf("second clear: %v", err)
		}
		if _, err := s.UserAuth(ctx, uid); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("after clear: %v", err)
		}
	})

	t.Run("ClearedOnDeviceReenroll", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		enroll(t, s, device(2), 2)
		if err := s.StoreUserAuthChallenge(ctx, uid, "n", nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreUserAuthChallenge(ctx, user(2, "bob"), "n", nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAuthenticate(ctx, device(1), auth("S1"), nil, t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UserAuth(ctx, uid); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("state survived device re-enrollment: %v", err)
		}
		if st, err := s.UserAuth(ctx, user(2, "bob")); err != nil || st.Challenge != "n" {
			t.Fatalf("other device's state touched: %+v %v", st, err)
		}
	})
}

// snapshot is what the source store held, captured before the target
// store is created (a shared-database factory resets its tables on every
// call, so the source is read completely first).
type snapshot struct {
	records   []storage.EnrollmentExport
	get       map[mdm.EnrollmentID]*storage.Enrollment
	history   []storage.CertAssociation
	bootstrap []byte
}

// RunMigrationSuite covers MigrationStore (decision record 0017). The
// factory is called twice so records move between two stores; the source
// is fully read before the target is created.
func RunMigrationSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	// seed builds a device with every field populated plus a user channel
	// and a disabled second device, and returns the export stream.
	seed := func(t *testing.T, s storage.Store) []storage.EnrollmentExport {
		t.Helper()
		enroll(t, s, device(1), 1)
		enroll(t, s, user(1, "u"), 2)
		enroll(t, s, device(2), 3)
		if err := s.AssociateCert(ctx, device(1), "h1", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCert(ctx, device(1), "h2", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreBootstrapToken(ctx, device(1), []byte("bst"), t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.Disable(ctx, device(2), t0.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		return exportAll(t, s, 0)
	}
	take := func(t *testing.T, s storage.Store) snapshot {
		t.Helper()
		snap := snapshot{records: seed(t, s), get: map[mdm.EnrollmentID]*storage.Enrollment{}}
		for _, r := range snap.records {
			e, err := s.Get(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			snap.get[r.ID] = e
		}
		snap.history, _ = s.CertHistory(ctx, device(1))
		snap.bootstrap, _ = s.BootstrapToken(ctx, device(1))
		return snap
	}

	t.Run("RoundTripAllFields", func(t *testing.T) {
		src := take(t, newStore(t))
		recs := src.records
		if len(recs) != 3 {
			t.Fatalf("exported %d records", len(recs))
		}
		b := newStore(t)
		for _, r := range recs {
			if err := b.Import(ctx, r); err != nil {
				t.Fatalf("Import %s: %v", r.ID.ID, err)
			}
		}
		for _, r := range recs {
			eb, err := b.Get(ctx, r.ID)
			if err != nil {
				t.Fatalf("Get %s after import: %v", r.ID.ID, err)
			}
			if ea := src.get[r.ID]; !sameEnrollment(*ea, *eb) {
				t.Fatalf("record %s differs after import:\n a=%+v\n b=%+v", r.ID.ID, ea, eb)
			}
		}
		if tok, err := b.BootstrapToken(ctx, device(1)); err != nil || string(tok) != "bst" || string(src.bootstrap) != "bst" {
			t.Fatalf("bootstrap token after import = %q %v", tok, err)
		}
		if _, err := b.BootstrapToken(ctx, device(2)); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("device 2 gained a bootstrap token: %v", err)
		}
		if owner, err := b.EnrollmentByCertHash(ctx, "h2"); err != nil || owner != device(1) {
			t.Fatalf("pin after import = %v %v", owner, err)
		}
		hb, _ := b.CertHistory(ctx, device(1))
		if len(hb) != 2 || len(src.history) != 2 || hb[0] != src.history[0] || hb[1] != src.history[1] {
			t.Fatalf("history after import = %+v, want %+v", hb, src.history)
		}
		info, _ := b.PushInfo(ctx, []mdm.EnrollmentID{device(1), device(2), user(1, "u")})
		if len(info) != 2 || info[device(1)].Magic != "magic-1" {
			t.Fatalf("push info after import = %+v", info)
		}
		if next, err := b.Next(ctx, device(1), false, t0); err != nil || next != nil {
			t.Fatalf("import touched the queue: %v %v", next, err)
		}
		// The second export equals the first.
		again := exportAll(t, b, 0)
		if len(again) != len(recs) {
			t.Fatalf("re-export has %d records", len(again))
		}
		for i := range recs {
			if !sameEnrollment(recs[i].Enrollment, again[i].Enrollment) || string(recs[i].BootstrapToken) != string(again[i].BootstrapToken) || len(recs[i].CertHistory) != len(again[i].CertHistory) {
				t.Fatalf("re-export %d differs:\n %+v\n %+v", i, recs[i], again[i])
			}
		}
	})

	t.Run("OrderParentsFirstAndPagination", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		for _, limit := range []int{1, 2} {
			seen := map[string]bool{}
			for _, r := range exportAll(t, s, limit) {
				if r.ID.Channel.IsUser() && !seen[r.ID.ParentID] {
					t.Fatalf("limit %d: user %s exported before its parent", limit, r.ID.ID)
				}
				if seen[r.ID.ID] {
					t.Fatalf("limit %d: %s exported twice", limit, r.ID.ID)
				}
				seen[r.ID.ID] = true
			}
			if len(seen) != 3 {
				t.Fatalf("limit %d: exported %d records", limit, len(seen))
			}
		}
		if _, err := s.Export(ctx, paging.Page{Cursor: "bogus"}); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("bad cursor: %v", err)
		}
	})

	t.Run("ImportRejects", func(t *testing.T) {
		recs := seed(t, newStore(t))
		b := newStore(t)
		var dev, usr storage.EnrollmentExport
		for _, r := range recs {
			switch r.ID {
			case device(1):
				dev = r
			case user(1, "u"):
				usr = r
			}
		}
		if err := b.Import(ctx, usr); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("orphan user channel: %v", err)
		}
		if err := b.Import(ctx, storage.EnrollmentExport{}); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
		foreign := dev
		foreign.CertHistory = []storage.CertAssociation{{ID: device(9), Hash: "h9", At: t0}}
		if err := b.Import(ctx, foreign); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("history for another id: %v", err)
		}
		userWithPin := usr
		userWithPin.CertHash = "h2"
		if err := b.Import(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if err := b.Import(ctx, userWithPin); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("user channel with device state: %v", err)
		}
		if err := b.Import(ctx, usr); err != nil {
			t.Fatalf("user after parent: %v", err)
		}
		// Idempotent.
		if err := b.Import(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if h, _ := b.CertHistory(ctx, device(1)); len(h) != 2 {
			t.Fatalf("history duplicated on re-import: %+v", h)
		}
		// A hash currently pinned elsewhere conflicts.
		enroll(t, b, device(3), 3)
		if err := b.AssociateCert(ctx, device(3), "h3", t0); err != nil {
			t.Fatal(err)
		}
		stolen := dev
		stolen.CertHash = "h3"
		if err := b.Import(ctx, stolen); !errors.Is(err, storage.ErrConflict) {
			t.Fatalf("pin held elsewhere: %v", err)
		}
	})
}

func exportAll(t *testing.T, s storage.Store, limit int) []storage.EnrollmentExport {
	t.Helper()
	ctx := context.Background()
	var out []storage.EnrollmentExport
	cursor := ""
	for {
		res, err := s.Export(ctx, paging.Page{Cursor: cursor, Limit: limit})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		out = append(out, res.Items...)
		if res.NextCursor == "" {
			return out
		}
		cursor = res.NextCursor
	}
}

// sameEnrollment compares two records field by field with time.Equal and
// nil-tolerant byte comparison.
func sameEnrollment(a, b storage.Enrollment) bool {
	times := [][2]time.Time{{a.EnrolledAt, b.EnrolledAt}, {a.TokenUpdatedAt, b.TokenUpdatedAt}, {a.LastSeenAt, b.LastSeenAt},
		{a.DisabledAt, b.DisabledAt}, {a.CertHashAt, b.CertHashAt}, {a.BootstrapTokenAt, b.BootstrapTokenAt}}
	for _, p := range times {
		if !p[0].Equal(p[1]) {
			return false
		}
	}
	return a.ID == b.ID && a.Enabled == b.Enabled && a.Push.Topic == b.Push.Topic && a.Push.Magic == b.Push.Magic &&
		bytes.Equal(a.Push.Token, b.Push.Token) && a.Device == b.Device && a.UserShortName == b.UserShortName &&
		a.UserLongName == b.UserLongName && bytes.Equal(a.UnlockToken, b.UnlockToken) && bytes.Equal(a.AuthenticateRaw, b.AuthenticateRaw) &&
		bytes.Equal(a.TokenUpdateRaw, b.TokenUpdateRaw) && a.CertHash == b.CertHash
}
