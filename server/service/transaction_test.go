package service_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestCheckinRequiredEventFailureDoesNotEnroll checks that checkin required event failure does not
// enroll.
func TestCheckinRequiredEventFailureDoesNotEnroll(t *testing.T) {
	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "mdm.sqlite"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	outbox, err := eventstore.Open(t.Context(), s.DB(), sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	p := &eventstore.Publisher{Store: outbox, Destinations: []string{"audit"}}
	c, err := service.New(service.Config{Store: s, Bus: p, Pinning: service.PinOff})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the required capture path unavailable while the domain tables work.
	if _, err = s.DB().ExecContext(t.Context(), "DROP TABLE event_records"); err != nil {
		t.Fatal(err)
	}
	result, err := c.Checkin(t.Context(), &mdm.Request{}, authenticate(t, "device"))
	if result != nil || !errors.Is(err, event.ErrCapture) || service.CodeOf(err) != service.CodeUnavailable {
		t.Fatal(result, err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	if _, err = s.Get(t.Context(), id); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("enrollment committed without its event", err)
	}
}
