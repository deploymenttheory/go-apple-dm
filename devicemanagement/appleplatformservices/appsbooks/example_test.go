package appsbooks_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
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
