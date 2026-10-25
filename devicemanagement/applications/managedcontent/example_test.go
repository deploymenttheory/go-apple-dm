package managedcontent_test

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ExampleWorkflow_Install shows the local-intent seam. Applications provide their
// existing durable state, registered location and authenticated native adapters.
func ExampleWorkflow_Install() {
	queue := func(ctx context.Context, store state.Store, licenses *appsbooks.Workflow, targets managedcontent.Resolver, delivery managedcontent.Delivery, selected managedcontent.Selection) (managedcontent.Operation, error) {
		workflow, err := managedcontent.New(store, licenses, targets, delivery)
		if err != nil {
			return managedcontent.Operation{}, err
		}
		// This can join the caller's required audit/state transaction. It performs
		// no Apple, resolver or command/declaration submission calls.
		return workflow.Install(ctx, "caller-persisted-request-id", managedcontent.Plan{
			LocationID: "registered-location",
			Content: managedcontent.Content{
				Asset: appsbooks.Asset{AdamID: "408709785", PricingParam: "STDQ"}, Kind: "App",
			},
			Target: selected, Method: managedcontent.MDM, MaxPages: 100,
		})
	}
	_ = queue
}

// ExampleWorkflow_Step keeps worker advancement outside the local API transaction.
// The caller owns scheduling, error classification and separate raw authorization.
func ExampleWorkflow_Step() {
	advance := func(ctx context.Context, workflow *managedcontent.Workflow, operationID string) error {
		operation, err := workflow.Step(ctx, operationID, "worker-identity", time.Minute)
		if err != nil {
			return err // Inspect ordinary safe status; do not publish raw errors.
		}
		if operation.Observation != nil {
			_ = operation.Observation.CommandAcknowledged
			_ = operation.Observation.DeclarationPublished
			_ = operation.Observation.Installed
			_ = operation.Observation.AppVersion
		}
		return nil
	}
	_ = advance
}
