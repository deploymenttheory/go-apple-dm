package correlation_test

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// ExampleCorrelation_Resolve demonstrates optional resolution without an inventory repository.
func ExampleCorrelation_Resolve() {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	// The caller retains a successful collector resource and authenticated native
	// reports. Authentication and persistence are outside this optional resolver.
	apple := correlation.AppleDevice{AccountID: "organization", ObservedAt: at.Add(time.Hour), Resource: collection.Resource{ID: "opaque-resource", Type: "orgDevices", Raw: json.RawMessage(`{"id":"opaque-resource","type":"orgDevices","attributes":{"serialNumber":"SERIAL","deviceModel":"Apple model"}}`)}}
	report := correlation.NativeReport{Source: source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:enrollment"}, Serial: "SERIAL", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true, ObservedAt: at, Fields: map[string]json.RawMessage{"ModelName": json.RawMessage(`"Device model"`)}, Raw: json.RawMessage(`{"ModelName":"Device model"}`)}
	matched, err := correlation.Correlate(apple, []correlation.NativeReport{report})
	if err != nil {
		panic(err)
	}
	// The host explicitly declares equivalent semantics; no default aliases exist.
	values, err := matched.Resolve([]correlation.Equivalent{{Name: "model", Inputs: []correlation.Field{{Source: matched.AppleReference(), Name: "deviceModel"}, {Source: report.Source, Name: "ModelName"}}}})
	if err != nil {
		panic(err)
	}
	fmt.Println(string(values["model"].Selected.Value), values["model"].Disagrees)
	// Output: "Device model" true
}
