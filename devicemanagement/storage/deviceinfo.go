package storage

import (
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// DeviceInfoFromResult refreshes routing metadata from an acknowledged,
// tracked device-channel inventory command. Missing or malformed observations
// preserve the previous values. Callers supply the stored request type, never
// a client-authored discriminator. User-channel results cannot change the
// parent device's platform or version.
func DeviceInfoFromResult(
	old DeviceInfo,
	id mdm.EnrollmentID,
	requestType string,
	response *mdm.Response,
) DeviceInfo {
	if id.Channel.IsUser() || response == nil || response.Status != mdm.StatusAcknowledged ||
		requestType != "DeviceInformation" {
		return old
	}
	decoded, err := mdm.DecodeResponse(response.Raw, requestType)
	if err != nil || decoded.Status != mdm.StatusAcknowledged ||
		decoded.CommandUUID != response.CommandUUID {
		return old
	}
	payload, ok := decoded.Payload.(*commands.DeviceInformationResponse)
	if !ok {
		return old
	}
	observed := payload.QueryResponses
	if observed.OSVersion != nil {
		if v, err := support.ParseVersion(*observed.OSVersion); err == nil && !v.IsZero() {
			old.OSVersion = *observed.OSVersion
		}
	}
	if observed.ProductName != nil && *observed.ProductName != "" {
		old.ProductName = *observed.ProductName
	}
	if observed.BuildVersion != nil && *observed.BuildVersion != "" {
		old.BuildVersion = *observed.BuildVersion
	}
	return old
}
