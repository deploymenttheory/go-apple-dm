package acmetest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
)

func runOrderTransactions(t *testing.T, factory Factory) {
	s := factory(t)
	ctx := t.Context()
	must(t, "seed order", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, Account("account")); err != nil {
			return err
		}
		return tx.PutOrder(ctx, Order("order", "account"))
	}))
	for _, id := range []string{"", "missing"} {
		want := acme.ErrNotFound
		if id == "" {
			want = acme.ErrInvalid
		}
		err := s.UpdateOrder(ctx, id, func(acme.Tx) error {
			t.Error("callback invoked without an existing order")
			return nil
		})
		wantErr(t, "invalid order", err, want)
	}
	wantErr(t, "nil callback", s.UpdateOrder(ctx, "order", nil), acme.ErrInvalid)
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			errs <- s.UpdateOrder(ctx, "order", func(tx acme.Tx) error {
				o, err := tx.GetOrder(ctx, "order")
				if err != nil {
					return err
				}
				o.CSRHash += "x"
				return tx.PutOrder(ctx, o)
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, "concurrent update", err)
	}
	o, err := s.GetOrder(ctx, "order")
	must(t, "read order", err)
	if len(o.CSRHash) != writers {
		t.Fatalf("lost order updates: %q", o.CSRHash)
	}
	err = s.UpdateOrder(ctx, "order", func(tx acme.Tx) error {
		o.CSRHash = "must roll back"
		if err := tx.PutOrder(ctx, o); err != nil {
			return err
		}
		return errBoom
	})
	wantErr(t, "rollback", err, errBoom)
	o, err = s.GetOrder(ctx, "order")
	must(t, "read rolled back order", err)
	if len(o.CSRHash) != writers {
		t.Fatal(fmt.Sprintf("failed transaction changed the receipt: %q", o.CSRHash))
	}
}
