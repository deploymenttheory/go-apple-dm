package proxywire

import "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"

const ConfigurationProfilePath = "/v1/configuration-profile"

// ConfigurationProfileRequest carries the enrollment identity and immutable profile
// revision across the authenticated private adapter.
type ConfigurationProfileRequest struct {
	Enrollment mdm.EnrollmentID
	Revision   string
}
