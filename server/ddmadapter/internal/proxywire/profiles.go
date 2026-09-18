package proxywire

import "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"

const ConfigurationProfilePath = "/v1/configuration-profile"

type ConfigurationProfileRequest struct {
	Enrollment mdm.EnrollmentID
	Revision   string
}
