package inventory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// ExampleRepository_Link keeps original resources independent and explicitly
// establishes a checked relationship using caller-authenticated enrollment facts.
func ExampleRepository_Link() {
	ctx := context.Background()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		panic(err)
	}
	if err := repo.Migrate(ctx, at); err != nil {
		panic(err)
	}
	appleRef := source.Reference{Kind: "axm.device", AccountID: "account", ResourceID: "opaque"}
	apple, err := inventory.ResourceObservation(appleRef, json.RawMessage(`{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER100"}}`), at, time.Hour)
	if err != nil {
		panic(err)
	}
	a, err := repo.Observe(ctx, "", apple)
	if err != nil {
		panic(err)
	}
	nativeRef := source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:enrollment"}
	native, err := inventory.ResourceObservation(nativeRef, json.RawMessage(`{"QueryResponses":{"SerialNumber":"SER100","Flag":false}}`), at, time.Hour)
	if err != nil {
		panic(err)
	}
	// These are facts established by the host's authenticated enrollment exchange,
	// never inferred from a channel ID or an Apple account name.
	native.Native = &inventory.NativeIdentity{Serial: "SER100", Method: correlation.ProfileBasedDeviceEnrollment, Authenticated: true}
	n, err := repo.Observe(ctx, "", native)
	if err != nil {
		panic(err)
	}
	link, err := repo.Link(ctx, appleRef, nativeRef, at)
	if err != nil {
		panic(err)
	}
	fmt.Println(a.ID != n.ID, link.Serial)
	localID, _ := a.Value("meta.id")
	originalID, _ := a.Value("data.id")
	fmt.Println(inventory.String(localID) == a.ID, inventory.String(originalID))
	// Output:
	// true SER100
	// true opaque
}
