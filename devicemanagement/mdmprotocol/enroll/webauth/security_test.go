package webauth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestCallbackRequiresOriginalBrowser(t *testing.T) {
	h := newHarness(t, nil)
	callback := h.callbackURL("/begin?serial=original")
	other := h.idp.Client(h.rp.Certificate())
	r, err := other.Get(context.Background(), callback)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("transferred callback: %d", r.StatusCode)
	}
	if len(h.completed) != 0 {
		t.Fatal("transferred callback completed enrollment")
	}
	r, err = h.view.Get(context.Background(), callback)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK || len(h.completed) != 1 {
		t.Fatalf("original browser lost state: %d", r.StatusCode)
	}
}

func TestParallelBrowserFlowsAndHEAD(t *testing.T) {
	h := newHarness(t, nil)
	a := h.callbackURL("/begin?serial=a")
	b := h.callbackURL("/begin?serial=b")
	r, err := h.rp.Client().Head(a)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD accepted: %d", r.StatusCode)
	}
	for _, url := range []string{b, a} {
		r, err := h.view.Get(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("parallel callback: %d", r.StatusCode)
		}
	}
}

func TestSharedBrowserBindingConsumption(t *testing.T) {
	ctx := context.Background()
	backend := state.NewMemory()
	first, second := &webauth.SharedStore{Backend: backend}, &webauth.SharedStore{Backend: backend}
	st := webauth.State{
		BrowserHash: "bound",
		Verifier:    "verifier",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	if err := first.Put(ctx, "flow", st); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Take(ctx, "flow", "wrong"); !errors.Is(err, webauth.ErrBrowserBinding) {
		t.Fatalf("binding: %v", err)
	}
	if got, err := second.Take(ctx, "flow", "bound"); err != nil || got.Verifier != st.Verifier {
		t.Fatalf("replica: %+v %v", got, err)
	}
	if _, err := first.Take(ctx, "flow", "bound"); !errors.Is(err, webauth.ErrStateNotFound) {
		t.Fatalf("replay: %v", err)
	}
}

func TestSharedBrowserStateRejectsInvalidAndDuplicateKeys(t *testing.T) {
	ctx := t.Context()
	empty := &webauth.SharedStore{}
	if err := empty.Put(ctx, "flow", webauth.State{}); !errors.Is(err, webauth.ErrStateKey) {
		t.Fatal(err)
	}
	if _, err := empty.Take(ctx, "flow", "browser"); !errors.Is(err, webauth.ErrStateKey) {
		t.Fatal(err)
	}
	store := &webauth.SharedStore{Backend: state.NewMemory()}
	st := webauth.State{BrowserHash: "original", ExpiresAt: time.Now().Add(time.Minute)}
	if err := store.Put(ctx, "flow", st); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(
		ctx,
		"flow",
		webauth.State{BrowserHash: "attacker"},
	); !errors.Is(
		err,
		webauth.ErrStateExists,
	) {
		t.Fatal(err)
	}
	if _, err := store.Take(ctx, "flow", "original"); err != nil {
		t.Fatal("duplicate overwrote binding", err)
	}
}
