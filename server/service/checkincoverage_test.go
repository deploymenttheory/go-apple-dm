package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// knownUnhandledCheckin records generated messages intentionally unsupported by
// the service. Each entry requires a reason; the current set is empty. The test
// checks real decoder and dispatcher behavior.
var knownUnhandledCheckin = map[string]string{}

// TestEveryCheckinMessageIsHandled pairs the generated check-in registry
// against what the service actually dispatches.
//
// This is the gap that let ReturnToService sit in the registry, decodable and
// documented, while a device that sent it got a 400 (decision record 0045).
// The generator had done its job perfectly; there was simply nothing comparing
// what Apple defines against what this server does, so a message type could
// arrive in a schema refresh and be missed.
//
// The test sends a real check-in through the plist decoder and the dispatcher
// rather than reading the source, so it cannot be satisfied by a case that
// falls through, and it fails on the next message Apple adds.
func TestEveryCheckinMessageIsHandled(t *testing.T) {
	t.Parallel()
	if len(checkin.Registry) == 0 {
		t.Fatal("the check-in registry is empty; the generated schema is missing")
	}
	for id := range checkin.Registry {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, service.Config{})
			const udid = "COVERAGE-UDID"
			enroll(t, h, udid)
			_, err := h.core.Checkin(context.Background(), req(h.cert), simple(t, id, udid, nil))
			unsupported := errors.Is(err, service.ErrInvalidMessage)
			reason, known := knownUnhandledCheckin[id]
			switch {
			case unsupported && !known:
				t.Errorf("Apple defines check-in %s and the generator emits it, but the service "+
					"answers %v; implement it, or record why not in knownUnhandledCheckin", id, err)
			case !unsupported && known:
				t.Errorf("check-in %s is handled now: delete it from knownUnhandledCheckin (%q)", id, reason)
			}
		})
	}
}
