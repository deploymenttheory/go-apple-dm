package ddm_test

import (
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

func TestPublishSet(t *testing.T) {
	ctx := t.Context()
	store := inmem.New()
	e, _ := ddm.New(ddm.Config{Store: store})
	raw := jsontext.Value(`{"Identifier":"config","Type":"com.apple.configuration.math.settings","Payload":{}}`)
	p := ddm.SetPublication{Name: "test", Declarations: []jsontext.Value{raw}}
	first, err := e.PublishSet(ctx, p)
	if err != nil || !first.Changed {
		t.Fatalf("first: %+v %v", first, err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	if _, err := e.AssignSet(ctx, id, "test"); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Manifest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.PublishSet(ctx, p)
	if err != nil || next.Changed {
		t.Fatalf("equivalent: %+v %v", next, err)
	}
	bad := ddm.SetPublication{Name: "test", Declarations: []jsontext.Value{raw, jsontext.Value(`{"Identifier":"bad","Type":"missing","Payload":{}}`)}}
	if _, err := e.PublishSet(ctx, bad); err == nil {
		t.Fatal("invalid publication succeeded")
	}
	if got, _ := e.SetDeclarations(ctx, "test"); len(got) != 1 {
		t.Fatal("failed publication changed membership")
	}
	if _, err := e.PublishSet(ctx, ddm.SetPublication{Name: "test"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.SetDeclarations(ctx, "test"); len(got) != 0 {
		t.Fatal("replacement retained omitted declaration")
	}
	if got, _ := e.EnrollmentSets(ctx, id); len(got) != 1 {
		t.Fatal("replacement lost assignment")
	}
	if _, err := store.GetDeclarationVersion(ctx, "config", snap.Items[0].ServerToken); err != nil {
		t.Fatal("replacement deleted advertised version", err)
	}
	err = store.Update(ctx, func(tx ddm.Tx) error {
		if _, err := e.PublishSetTx(ctx, tx, p); err != nil {
			return err
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("transaction succeeded")
	}
	if got, _ := e.SetDeclarations(ctx, "test"); len(got) != 0 {
		t.Fatal("rollback left membership")
	}
}

func TestPublishSetRollsBackStorageFailures(t *testing.T) {
	for _, operation := range []string{"LockPublication", "PutSet", "SetDeclarations", "GetDeclaration", "PutDeclaration", "AddSetDeclaration", "RemoveSetDeclaration", "AffectedEnrollments", "RecordChanges"} {
		t.Run(operation, func(t *testing.T) {
			store := inmem.New()
			failing := &ddmtest.Failing{Store: store, Fail: map[string]error{}}
			engine, err := ddm.New(ddm.Config{Store: failing})
			if err != nil {
				t.Fatal(err)
			}
			initial := jsontext.Value(`{"Identifier":"old","Type":"com.apple.configuration.math.settings","Payload":{}}`)
			if _, err := engine.PublishSet(t.Context(), ddm.SetPublication{Name: "apps", Declarations: []jsontext.Value{initial}}); err != nil {
				t.Fatal(err)
			}
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			if _, err := engine.AssignSet(t.Context(), id, "apps"); err != nil {
				t.Fatal(err)
			}
			before, err := engine.Manifest(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("publication storage unavailable")
			failing.Fail[operation] = failure
			replacement := jsontext.Value(`{"Identifier":"new","Type":"com.apple.configuration.math.settings","Payload":{}}`)
			result, err := engine.PublishSet(t.Context(), ddm.SetPublication{Name: "apps", Declarations: []jsontext.Value{replacement}})
			if !errors.Is(err, failure) || result.Changed || result.Name != "" || len(result.Declarations) != 0 {
				t.Fatalf("failed publication exposed a committed result: %+v, %v", result, err)
			}
			delete(failing.Fail, operation)
			members, err := engine.SetDeclarations(t.Context(), "apps")
			if err != nil || !reflect.DeepEqual(members, []string{"old"}) {
				t.Fatalf("failed publication changed membership: %v, %v", members, err)
			}
			if _, err := store.GetDeclaration(t.Context(), "new"); !errors.Is(err, ddm.ErrNotFound) {
				t.Fatalf("partial declaration persisted: %v", err)
			}
			after, err := engine.Manifest(t.Context(), id)
			if err != nil || before.DeclarationsToken != after.DeclarationsToken {
				t.Fatalf("failed publication changed the device token: %v", err)
			}
		})
	}
}

func TestPublishSetRejectsInvalidInputBeforeTransactionUse(t *testing.T) {
	engine, err := ddm.New(ddm.Config{Store: inmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	raw := jsontext.Value(`{"Identifier":"config","Type":"com.apple.configuration.math.settings","Payload":{}}`)
	for _, test := range []struct {
		publication ddm.SetPublication
		want        error
	}{
		{ddm.SetPublication{}, ddm.ErrInvalid},
		{ddm.SetPublication{Name: "apps", Declarations: []jsontext.Value{raw, raw}}, ddm.ErrInvalidDeclaration},
		{ddm.SetPublication{Name: "apps", Declarations: []jsontext.Value{activationWithPredicate("activation", "invalid predicate !!!", "config")}}, ddm.ErrInvalidDeclaration},
		// A transaction that cannot serialize publishers must reject publication.
		{ddm.SetPublication{Name: "apps", Declarations: []jsontext.Value{raw}}, ddm.ErrInvalid},
	} {
		if _, err := engine.PublishSetTx(t.Context(), nil, test.publication); !errors.Is(err, test.want) {
			t.Fatalf("%+v: %v, want %v", test.publication, err, test.want)
		}
	}
}
