package service_test

import (
	"context"
	"crypto/x509"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestInventoryObserverRollback verifies projection failures roll back tracked results and enrollment mutations.
func TestInventoryObserverRollback(t *testing.T) {
	for _, stage := range []string{"enrollment", "certificate", "result"} {
		t.Run(stage, func(t *testing.T) {
			ctx := t.Context()
			s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "observer.db"), sqlite.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			outbox, err := eventstore.Open(ctx, s.DB(), sqlite.Dialect)
			if err != nil {
				t.Fatal(err)
			}
			fail := false
			boom := errors.New("projection unavailable")
			c, err := service.New(service.Config{
				Store: s, Bus: &eventstore.Publisher{Store: outbox}, Pinning: service.PinOff,
				ObserveEnrollment: func(context.Context, mdm.EnrollmentID) error {
					if fail && stage == "enrollment" {
						return boom
					}
					return nil
				},
				ObserveCertificate: func(context.Context, mdm.EnrollmentID, *x509.Certificate, time.Time) error {
					if fail && stage == "certificate" {
						return boom
					}
					return nil
				},
				ObserveResult: func(context.Context, mdm.EnrollmentID, *mdm.Response, time.Time) error {
					if fail && stage == "result" {
						return boom
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Checkin(ctx, &mdm.Request{}, authenticate(t, "device")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Checkin(ctx, &mdm.Request{}, tokenUpdate(t, "device", nil)); err != nil {
				t.Fatal(err)
			}
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			cmd := newCmd(t, &commands.DeviceInformation{Queries: []string{"OSVersion"}})
			if _, err := c.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Connect(ctx, &mdm.Request{}, response(id.ID, "", mdm.StatusIdle)); err != nil {
				t.Fatal(err)
			}
			fail = true
			if _, err := c.Connect(ctx, &mdm.Request{Certificate: &x509.Certificate{}}, response(id.ID, cmd.UUID, mdm.StatusAcknowledged)); !errors.Is(err, boom) {
				t.Fatal(err)
			}
			rows, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{Limit: 10})
			if err != nil || len(rows.Items) != 1 || rows.Items[0].Result != nil {
				t.Fatal("result committed after projection failure", rows, err)
			}
			if stage != "result" {
				if _, err := c.Checkin(ctx, &mdm.Request{Certificate: &x509.Certificate{}}, authenticate(t, "new-device")); !errors.Is(err, boom) {
					t.Fatal(err)
				}
				if _, err := s.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "new-device"}); !errors.Is(err, storage.ErrNotFound) {
					t.Fatal("failed projection enrolled device", err)
				}
			}
		})
	}
}
