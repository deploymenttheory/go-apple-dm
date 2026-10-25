package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/adetest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

func testADEParsed(t *testing.T) *ade.Parsed {
	t.Helper()
	chain := adetest.NewChain(t)
	blob := adetest.Sign(t, chain, adetest.Info("ADE-DURABLE"), adetest.SignOptions{SignedAttributes: true})
	p, err := ade.ParseMachineInfo(adetest.Request(t, "https://mdm.example", blob, adetest.LaneHeader), chain.Options())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestADEEvidenceState(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now().UTC()
			var first, second state.Store
			if backend == "memory" {
				m := state.NewMemory()
				m.Now = func() time.Time { return now }
				first, second = m, m
			} else {
				db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "state.sqlite"), sqlite.Options{}))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				ring, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "test"}, Provider: secrets.Static{"test": bytes.Repeat([]byte{3}, 32)}})
				if err != nil {
					t.Fatal(err)
				}
				first, err = statestore.Open(ctx, db, sqlite.Dialect, ring)
				if err != nil {
					t.Fatal(err)
				}
				second, err = statestore.Open(ctx, db, sqlite.Dialect, ring)
				if err != nil {
					t.Fatal(err)
				}
			}
			exerciseADEEvidenceState(t, first, second, now)
		})
	}
}

// exerciseADEEvidenceState runs the same durable-evidence contract against every
// protocol backend; separate store objects represent independent replicas.
func exerciseADEEvidenceState(t *testing.T, first, second state.Store, now time.Time) {
	t.Helper()
	ctx := t.Context()
	p := testADEParsed(t)
	writer, reader := adeStateStore{first}, adeStateStore{second}
	if err := writer.Put(ctx, &ade.Record{Parsed: p, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := reader.Get(ctx, p.SERIAL)
	if err != nil || !ok || !rec.Parsed.Verified || !bytes.Equal(rec.Parsed.Signer.Raw, p.Signer.Raw) || !bytes.Equal(rec.Parsed.Raw, p.Raw) {
		t.Fatalf("resume: %v %v", ok, err)
	}
	if _, ok, err := reader.Get(ctx, "missing"); ok || err != nil {
		t.Fatal(ok, err)
	}
	e, err := evidenceFor(p, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	bound := webauth.Bound{Serial: p.SERIAL, UDID: p.UDID, Extra: map[string]string{"flow": "ade", "machineinfo": string(b)}}
	start := adeBrowserStore{webauth.SharedStore{Backend: first}}
	finish := adeBrowserStore{webauth.SharedStore{Backend: second}}
	expires := now.Add(webauth.DefaultStateTTL)
	if err = start.Put(ctx, "pending", webauth.State{BrowserHash: "browser", Bound: bound, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	// A later request for the same serial must not replace this browser's UDID.
	chain := adetest.NewChain(t)
	changedInfo := p.MachineInfo
	changedInfo.UDID = "other"
	changedBlob := adetest.Sign(t, chain, changedInfo, adetest.SignOptions{SignedAttributes: true})
	changed, err := ade.ParseMachineInfo(adetest.Request(t, "https://mdm.example", changedBlob, adetest.LaneHeader), chain.Options())
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Put(ctx, &ade.Record{Parsed: changed, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = finish.Take(ctx, "pending", "wrong-browser"); !errors.Is(err, webauth.ErrBrowserBinding) {
		t.Fatal(err)
	}
	st, err := finish.Take(ctx, "pending", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(st.Bound.Extra["machineinfo"]), &e); err != nil {
		t.Fatal(err)
	}
	if !e.ExpiresAt.Equal(st.ExpiresAt) {
		t.Fatal("different authentication lifetime")
	}
	loaded, err := e.parsed(t.Context(), st.Bound.Serial, st.Bound.UDID, now)
	if err != nil || loaded.UDID != p.UDID {
		t.Fatal(err)
	}
	if _, err = finish.Take(ctx, "pending", "browser"); !errors.Is(err, webauth.ErrStateNotFound) {
		t.Fatal("replayed", err)
	}
	for name, mutate := range map[string]func(*adeEvidence){
		"signature":      func(e *adeEvidence) { e.Raw = bytes.ReplaceAll(e.Raw, []byte("ADE-DURABLE"), []byte("ADE-CORRUPT")) },
		"missing signer": func(e *adeEvidence) { e.Signer = nil }, "corrupt signer": func(e *adeEvidence) { e.Signer = []byte("bad") },
		"unverified": func(e *adeEvidence) { e.Verified = false }, "missing time": func(e *adeEvidence) { e.ReceivedAt = time.Time{} },
		"origin":            func(e *adeEvidence) { e.Origin = "invalid" },
		"different signer":  func(e *adeEvidence) { e.Signer = changed.Signer.Raw },
		"inverted lifetime": func(e *adeEvidence) { e.ReceivedAt = e.ExpiresAt.Add(time.Second) },
		"expired":           func(e *adeEvidence) { e.ExpiresAt = now }, "missing expiration": func(e *adeEvidence) { e.ExpiresAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := e
			mutate(&bad)
			if _, err := bad.parsed(t.Context(), p.SERIAL, p.UDID, now); err == nil {
				t.Fatal("invalid retained evidence accepted")
			}
		})
	}
	for _, binding := range []struct{ serial, udid string }{{"different", p.UDID}, {p.SERIAL, "different"}} {
		if _, err = e.parsed(t.Context(), binding.serial, binding.udid, now); err == nil {
			t.Fatal("identity mismatch accepted")
		}
	}
	if err = start.Put(ctx, "concurrent", webauth.State{BrowserHash: "browser", Bound: bound, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := finish.Take(ctx, "concurrent", "browser"); results <- err }()
	}
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, webauth.ErrStateNotFound) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("one-use evidence consumed multiple times", successes)
	}
	// Failed transactions leave the original pending evidence intact.
	key := adeStateKey(p.SERIAL)
	if err = first.Update(ctx, []string{key}, func(tx state.Tx) error {
		if err := tx.Delete(ctx, key); err != nil {
			return err
		}
		return context.Canceled
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, ok, err = reader.Get(ctx, p.SERIAL); !ok || err != nil {
		t.Fatal("rollback lost evidence", ok, err)
	}
	for _, failure := range []string{"expiry mismatch", "expired"} {
		if err = first.Update(ctx, []string{key}, func(tx state.Tx) error {
			record, err := tx.Get(ctx, key)
			if err != nil {
				return err
			}
			var retained adeEvidence
			if err = json.Unmarshal(record.Value, &retained); err != nil {
				return err
			}
			record.ExpiresAt = tx.Now().Add(-time.Second)
			if failure == "expired" {
				retained.ExpiresAt = record.ExpiresAt
				retained.ReceivedAt = record.ExpiresAt.Add(-time.Minute)
			}
			record.Value, err = json.Marshal(retained)
			if err != nil {
				return err
			}
			return tx.Put(ctx, record)
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err = reader.Get(ctx, p.SERIAL); err == nil {
			t.Fatal("invalid stored expiry accepted", failure)
		}
		if err = writer.Put(ctx, &ade.Record{Parsed: p, ReceivedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err = first.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte("corrupt")}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err = reader.Get(ctx, p.SERIAL); err == nil {
		t.Fatal("corrupt persisted evidence accepted")
	}
}

func TestADECompletionRejectsMissingBinding(t *testing.T) {
	p := testADEParsed(t)
	now := time.Now()
	e, err := evidenceFor(p, now)
	if err != nil {
		t.Fatal(err)
	}
	e.ExpiresAt = now.Add(time.Minute)
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, bound := range []webauth.Bound{{Serial: "other", UDID: p.UDID}, {Serial: p.SERIAL, UDID: "other"}, {Serial: p.SERIAL}, {UDID: p.UDID}} {
		bound.Extra = map[string]string{"flow": "ade", "machineinfo": string(b)}
		enrollment := &enrollment{now: func() time.Time { return now }}
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/callback", nil)
		enrollment.complete(t.Context(), bound, webauth.Claims{}, webauth.Decision{}, w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal("binding accepted", w.Code)
		}
	}
	if _, err = evidenceFor(nil, now); err == nil {
		t.Fatal("missing evidence accepted")
	}
}

func TestADEEvidenceStorageFailures(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	p := testADEParsed(t)
	backend := state.NewMemory()
	store := adeStateStore{backend}
	for _, record := range []*ade.Record{nil, {}, {Parsed: &ade.Parsed{}}, {Parsed: &ade.Parsed{MachineInfo: ade.MachineInfo{SERIAL: "missing-CMS"}}}, {Parsed: p, ReceivedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		if err := store.Put(ctx, record); err == nil {
			t.Fatal("invalid or unencodable evidence stored")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Put(canceled, &ade.Record{Parsed: p, ReceivedAt: now}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := store.Get(canceled, p.SERIAL); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	browser := adeBrowserStore{webauth.SharedStore{Backend: backend}}
	bound := webauth.Bound{Serial: p.SERIAL, UDID: p.UDID, Extra: map[string]string{"flow": "ade", "machineinfo": "corrupt"}}
	if err := browser.Put(ctx, "invalid", webauth.State{Bound: bound}); err == nil {
		t.Fatal("corrupt browser evidence persisted")
	}
	e, err := evidenceFor(p, now)
	if err != nil {
		t.Fatal(err)
	}
	invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	// The internal writer rejects non-JSON timestamps; no partial browser grant is committed.
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	bound.Extra["machineinfo"] = string(b)
	if err = browser.Put(ctx, "invalid-time", webauth.State{Bound: bound, ExpiresAt: invalidTime}); err == nil {
		t.Fatal("unencodable browser lifetime persisted")
	}
	if _, err = browser.Take(ctx, "invalid-time", ""); !errors.Is(err, webauth.ErrStateNotFound) {
		t.Fatal("failed write left a grant", err)
	}
	chain := adetest.NewChain(t)
	info := p.MachineInfo
	info.UDID = ""
	raw := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: true})
	parsed, err := ade.ParseMachineInfo(adetest.Request(t, "https://mdm.example", raw, adetest.LaneHeader), chain.Options())
	if err != nil {
		t.Fatal(err)
	}
	malformed, err := evidenceFor(parsed, now)
	if err != nil {
		t.Fatal(err)
	}
	malformed.ExpiresAt = now.Add(time.Minute)
	if _, err = malformed.parsed(t.Context(), parsed.SERIAL, "", now); !errors.Is(err, ade.ErrPresence) {
		t.Fatal("incomplete signed device evidence accepted", err)
	}
}
