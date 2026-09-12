package ddm_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

type auditExpander func(context.Context, mdm.EnrollmentID, *ddm.Declaration) ([]byte, error)

func (f auditExpander) Expand(
	c context.Context,
	id mdm.EnrollmentID,
	d *ddm.Declaration,
) ([]byte, error) {
	return f(c, id, d)
}

func TestDeletedExpandedDeclarationNotServed(t *testing.T) {
	ctx := t.Context()
	st := ddminmem.New()
	e, err := ddm.New(
		ddm.Config{
			Store: st,
			Expander: auditExpander(
				func(_ context.Context, _ mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
					return bytes.ReplaceAll(
						d.Canonical,
						[]byte("placeholder"),
						[]byte("synthetic-private-value"),
					), nil
				},
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := e.PutDeclaration(
		ctx,
		[]byte(
			`{"Type":"com.apple.configuration.management.test","Identifier":"audit.deleted","Payload":{"Echo":"placeholder"}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "audit-device"}
	if _, err = e.AssignDeclaration(ctx, id, d.Identifier); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Manifest(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = e.DeleteDeclaration(ctx, d.Identifier); err != nil {
		t.Fatal(err)
	}
	if _, err = e.GetDeclaration(ctx, d.Identifier); err == nil {
		t.Fatal("deletion did not take effect")
	}
	got, err := e.Declaration(ctx, id, schemaddm.KindConfiguration, d.Identifier)
	if !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("deleted declaration served: %s, error: %v", got, err)
	}
}
