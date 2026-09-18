package ddm_test

import (
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
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
