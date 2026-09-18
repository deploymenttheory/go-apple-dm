package service_test

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestAuthorizeResource(t *testing.T) {
	ctx := t.Context()
	ca, err := testpki.NewCA("resources")
	if err != nil {
		t.Fatal(err)
	}
	one, err := ca.Issue("one", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	two, err := ca.Issue("two", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	st := inmem.New()
	id := deviceID("parent")
	user := mdm.EnrollmentID{ID: "parent:alice", ParentID: "parent", Channel: mdm.ChannelUser}
	for _, enrollment := range []storage.Enrollment{{ID: id, Enabled: true, CertHash: cms.Fingerprint(one.Cert)}, {ID: user, Enabled: true}} {
		if err := st.Import(ctx, storage.EnrollmentExport{Enrollment: enrollment}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := service.New(service.Config{Store: st, Pinning: service.PinOff})
	if err != nil {
		t.Fatal(err)
	}
	for _, enrollment := range []mdm.EnrollmentID{id, user} {
		if err := c.AuthorizeResource(ctx, &mdm.Request{ID: enrollment, Certificate: one.Cert}); err != nil {
			t.Fatal(err)
		}
	}
	wrongParent := user
	wrongParent.ParentID = "other"
	wrongChannel := id
	wrongChannel.Channel = mdm.ChannelUserEnrollmentDevice
	for _, r := range []*mdm.Request{nil, {ID: id}, {ID: id, Certificate: two.Cert}, {ID: wrongParent, Certificate: one.Cert}, {ID: wrongChannel, Certificate: one.Cert}} {
		if err := c.AuthorizeResource(ctx, r); err == nil {
			t.Fatal("resource authentication bypass", r)
		}
	}
	revoked := errors.New("revoked")
	c, err = service.New(service.Config{Store: st, CertificateStatus: func(context.Context, *x509.Certificate) error { return revoked }})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AuthorizeResource(ctx, &mdm.Request{ID: id, Certificate: one.Cert}); !errors.Is(err, revoked) {
		t.Fatal("revocation", err)
	}
	c, err = service.New(service.Config{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Disable(ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := c.AuthorizeResource(ctx, &mdm.Request{ID: user, Certificate: one.Cert}); !errors.Is(err, storage.ErrDisabled) {
		t.Fatal("disabled parent", err)
	}
}
