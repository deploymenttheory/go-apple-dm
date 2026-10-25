package appsbooks_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errLicensingIncomplete = errors.New("licensing incomplete: reconcile before resubmitting")

// installationAfterFallback is invoked by the caller's persisted job after no
// complete notification outcome arrived within five minutes. It never repeats
// the assignment mutation. For user licensing, first establish Associated user
// state using USER_ASSOCIATED or Users, then verify licensing in the same way.
func installationAfterFallback(
	ctx context.Context,
	c *appsbooks.Client,
	eventID, adamID string,
) (*commands.InstallApplication, error) {
	for attempts := 0; attempts < 11; attempts++ {
		status, err := c.EventStatus(ctx, eventID)
		if err != nil {
			return nil, err
		}
		if status.Successful() {
			id, err := strconv.ParseInt(adamID, 10, 64)
			if err != nil {
				return nil, err
			}
			purchase := int64(1) // Volume purchase license.
			return &commands.InstallApplication{
				ITunesStoreID: &id,
				Options:       &commands.InstallApplicationOptions{PurchaseMethod: &purchase},
			}, nil
		}
		if status.EventStatus != "PENDING" || attempts == 10 {
			return nil, errLicensingIncomplete
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errLicensingIncomplete
}

// ExampleClient_Associate demonstrates associating an Apps and Books asset with device serial
// numbers.
func ExampleClient_Associate() {
	c, err := appsbooks.New(
		appsbooks.Config{
			SToken: os.Getenv("APPSBOOKS_TOKEN"),
			MDMID:  "your-persisted-mdm-id",
			UID:    "your-location-uid",
		},
	)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Claim/configure the location separately using ClientConfig/SetClientConfig.
	// Obtain the AssetRecord first and check CheckAssignment(true,false).
	event, err := c.Associate(
		ctx,
		appsbooks.ManageAssetsRequest{
			Assets:        []appsbooks.Asset{{AdamID: "123456789", PricingParam: "STDQ"}},
			SerialNumbers: []string{"DEVICE-SERIAL"},
		},
	)
	if err != nil {
		panic(err)
	}
	// Persist event.UID and event.EventID with the requested assignments. Apply
	// authenticated notifications idempotently using (UID, Notification.ID).
	// Use installationAfterFallback in the persisted job after five minutes
	// without a complete outcome. Enqueue only its successful command result.
	_ = event
	_ = installationAfterFallback
}

// ExampleClient_WalkAssets keeps inventory availability separate from licensing completion.
func ExampleClient_WalkAssets() {
	c, err := appsbooks.New(appsbooks.Config{SToken: os.Getenv("APPSBOOKS_TOKEN"), MDMID: "your-persisted-mdm-id", UID: "your-location-uid"})
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	version, err := c.WalkAssets(ctx, appsbooks.AssetsQuery{Unlimited: new(true)}, 100, func(item appsbooks.AssetInventory) error {
		switch item.Stock().Kind {
		case appsbooks.StockUnlimited:
			// Unlimited stock is not a finite available-license count.
		case appsbooks.StockFinite:
			_ = item.Stock().Available // Includes a reported zero.
		case appsbooks.StockUnknown:
			// Missing counts require a later observation, not an assumed zero.
		}
		if item.Record != nil {
			_ = item.Record.Raw
		} else {
			_ = item.Unlimited.Raw
		}
		// Persist the original representation in the same location scope as its sToken.
		return nil
	})
	if err != nil {
		panic(err)
	}
	_ = version // Assets do not support incremental queries.
}

// ExampleWorkflow shows optional atomic location composition. Real applications
// supply durable sealed state and their own authorization for source exports.
func ExampleWorkflow() {
	ctx := context.Background()
	workflow, err := appsbooks.NewWorkflow(state.NewMemory(), appsbooks.Config{MDMID: "persisted-mdm-identity"})
	if err != nil {
		panic(err)
	}
	// Register probes identity without claiming ownership. For renewal, pass the
	// revision from Load and the replacement sToken for this same Apple library.
	location, err := workflow.Register(ctx, "london", 0, os.Getenv("APPSBOOKS_STOKEN"))
	if err != nil {
		return
	}
	location, err = workflow.Configure(ctx, location.ID, location.Revision, appsbooks.ClientConfigurationRequest{
		MDMInfo: appsbooks.MDMInfo{ID: "persisted-mdm-identity", Name: "Example MDM", Metadata: "production"},
	})
	if err != nil {
		return
	} // Inspect Load; Probe reconciles a stopped pending call.
	location, err = workflow.Refresh(ctx, location.ID, location.Revision, 100)
	if err != nil {
		return
	} // Prior successful evidence/checkpoints remain intact.
	_, sources, err := workflow.LoadEvidence(ctx, location.ID) // Privileged source access.
	if err != nil {
		return
	}
	_ = sources.Assets.Pages // Original JSON, including sentinel/unlimited assets.
	_ = sources.Assignments  // Ordered full + incremental batches, with checkpoints.
	// Refresh establishes source evidence; licensing and installation are separate.
}

// ExampleWorkflow_RecoverConfiguration runs only after the original configuration
// call/process has stopped. Use the existing durable store containing its pending
// reservation; memory below illustrates the caller-owned storage interface.
func ExampleWorkflow_RecoverConfiguration() {
	ctx := context.Background()
	workflow, err := appsbooks.NewWorkflow(state.NewMemory(), appsbooks.Config{MDMID: "persisted-mdm-identity"})
	if err != nil {
		panic(err)
	}
	location, err := workflow.Load(ctx, "london")
	if err != nil {
		return
	}
	// The replacement must identify the same Apple library, including when the
	// stored token expired. No configuration POST is replayed.
	_, err = workflow.RecoverConfiguration(ctx, location.ID, location.Revision, os.Getenv("APPSBOOKS_REPLACEMENT_STOKEN"))
	if err != nil {
		return
	} // Pending state and prior evidence remain available.
}
