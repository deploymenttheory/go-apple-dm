package storetest

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Run proves replica visibility, enrollment binding, pagination, rotation,
// revocation, retention and hashed credential storage. a and b share storage.
func Run(t *testing.T, a, b state.Store) {
	t.Helper()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprintf("cache-%d", time.Now().UnixNano())}
	other := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: id.ID + "-other"}
	x, y := &contentcache.StateStore{State: a}, &contentcache.StateStore{State: b}
	token, err := x.RotateCredential(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := y.Authenticate(t.Context(), token); err != nil || got != id {
		t.Fatalf("replica auth: %v %v", got, err)
	}
	rows, err := b.List(t.Context(), "contentcache/v1/credential/", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if bytes.Contains(row.Value, []byte(token)) {
			t.Fatal("plaintext credential persisted")
		}
	}
	report := &contentcache.Report{Version: new(int64(1)), ReportDate: new("2026-09-16T12:00:00Z"), Hostname: new("untrusted-host"), Hardware: new("Mac16,1"), ServerGUID: new("13D4D110-B2B7-4F26-8E25-CD22E58C00EE")}
	for range 3 {
		if err := y.Accept(t.Context(), token, report); err != nil {
			t.Fatal(err)
		}
	}
	first, err := x.Reports(t.Context(), id, paging.Page{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || first.Items[0].Enrollment != id {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := y.Reports(t.Context(), id, paging.Page{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.NextCursor != "" || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second: %+v %v", second, err)
	}
	if _, err := x.Reports(t.Context(), other, paging.Page{Cursor: first.NextCursor}); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("cross-enrollment cursor: %v", err)
	}
	for _, page := range []paging.Page{{Limit: -1}, {Limit: 1001}, {Cursor: "%%%"}} {
		if _, err := x.Reports(t.Context(), id, page); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("bad page: %v", err)
		}
	}
	newToken, err := y.RotateCredential(t.Context(), id)
	if err != nil || token == newToken {
		t.Fatal("rotation", err)
	}
	if err := x.Accept(t.Context(), token, report); !errors.Is(err, contentcache.ErrCredential) {
		t.Fatal("old credential accepted", err)
	}
	if err := x.RevokeCredential(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := y.Authenticate(t.Context(), newToken); !errors.Is(err, contentcache.ErrCredential) {
		t.Fatal("revoked credential accepted", err)
	}
	if err := x.RevokeCredential(t.Context(), id); err != nil {
		t.Fatal("revoke not idempotent", err)
	}
	for _, token := range []string{"", "x.y", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := x.Authenticate(t.Context(), token); !errors.Is(err, contentcache.ErrCredential) {
			t.Fatal("invalid credential accepted", err)
		}
	}
	if _, err := x.RotateCredential(t.Context(), mdm.EnrollmentID{}); !errors.Is(err, state.ErrInvalid) {
		t.Fatal("invalid identity accepted", err)
	}
	if err := x.Accept(t.Context(), token, nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatal("nil report accepted", err)
	}
	if err := x.Accept(t.Context(), token, &contentcache.Report{}); !errors.Is(err, contentcache.ErrInvalidReport) {
		t.Fatal("invalid report accepted", err)
	}
	// Force existing report expiry using the same record locks used by Accept.
	records, err := b.List(t.Context(), "contentcache/v1/reports/", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := a.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error {
			record.ExpiresAt = tx.Now().Add(-time.Second)
			return tx.Put(t.Context(), record)
		}); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := y.Reports(t.Context(), id, paging.Page{})
	if err != nil || len(remaining.Items) != 0 {
		t.Fatalf("expired reports visible: %+v %v", remaining, err)
	}
}
