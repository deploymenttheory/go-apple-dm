package ddmtest

import (
	"encoding/json/jsontext"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
)

// RunPublicationSuite exercises the optional publication locking contract.
func RunPublicationSuite(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("ReplaceRollbackAndConcurrency", func(t *testing.T) {
		ctx := t.Context()
		st := newStore(t)
		e, err := ddm.New(ddm.Config{Store: st})
		if err != nil {
			t.Fatal(err)
		}
		a := ddm.SetPublication{Name: "publication", Declarations: []jsontext.Value{jsontext.Value(`{"Identifier":"a","Type":"com.apple.configuration.math.settings","Payload":{}}`)}}
		b := ddm.SetPublication{Name: "publication", Declarations: []jsontext.Value{jsontext.Value(`{"Identifier":"b","Type":"com.apple.configuration.math.settings","Payload":{}}`)}}
		if _, err := e.PublishSet(ctx, a); err != nil {
			t.Fatal(err)
		}
		if _, err := e.AssignSet(ctx, Device(1), a.Name); err != nil {
			t.Fatal(err)
		}
		before, err := st.PendingChanges(ctx, time.Now().Add(time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := e.PublishSet(ctx, a); err != nil || result.Changed {
			t.Fatal(result, err)
		}
		after, err := st.PendingChanges(ctx, time.Now().Add(time.Hour), 100)
		if err != nil || len(before) != len(after) {
			t.Fatal("idempotence queued work", err)
		}
		err = st.Update(ctx, func(tx ddm.Tx) error {
			if _, err := e.PublishSetTx(ctx, tx, b); err != nil {
				return err
			}
			return errRollback
		})
		if !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
		if ids, err := e.SetDeclarations(ctx, a.Name); err != nil || len(ids) != 1 || ids[0] != "a" {
			t.Fatal("rollback", ids, err)
		}
		var wg sync.WaitGroup
		for i := range 6 {
			wg.Go(func() {
				p := a
				if i%2 == 0 {
					p = b
				}
				if _, err := e.PublishSet(ctx, p); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if ids, err := e.SetDeclarations(ctx, a.Name); err != nil || len(ids) != 1 {
			t.Fatal("partial or combined publication", ids, err)
		}
		if sets, err := e.EnrollmentSets(ctx, Device(1)); err != nil || len(sets) != 1 {
			t.Fatal("lost assignment", sets, err)
		}
	})
}
