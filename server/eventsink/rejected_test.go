package eventsink_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

func TestRejectedCommandProjection(t *testing.T) {
	t.Parallel()
	record := eventsink.Default().
		Project(event.Event{Type: event.CommandRejected, Data: map[string]any{
			"command_uuid": "command",
			"request_type": "AvailableOSUpdates",
			"reason":       "unsupported-target",
			"raw":          "private payload",
		}})
	if len(record.Fields) != 3 || record.Fields["command_uuid"] != "command" ||
		record.Fields["reason"] != "unsupported-target" {
		t.Fatalf("rejection projection: %+v", record.Fields)
	}
}
