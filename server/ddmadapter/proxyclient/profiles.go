package proxyclient

import (
	"context"
	json "encoding/json/v2"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/internal/proxywire"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// ConfigurationProfileFetcher runs only after the MDM ingress authenticates the full identity.
type ConfigurationProfileFetcher func(context.Context, mdm.EnrollmentID, string) (service.DMResponse, error)

// ConfigurationProfiles uses the authenticated private hop for profile delivery.
func ConfigurationProfiles(cfg Config) (ConfigurationProfileFetcher, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	c.target = strings.TrimSuffix(c.target, proxywire.Path) + proxywire.ConfigurationProfilePath
	c.contentType = "application/json"
	c.cfg.MaxBody = configurationprofile.MaxBytes
	return func(ctx context.Context, id mdm.EnrollmentID, revision string) (service.DMResponse, error) {
		body, err := json.Marshal(proxywire.ConfigurationProfileRequest{Enrollment: id, Revision: revision})
		if err != nil {
			return service.DMResponse{}, err
		}
		return c.handle(ctx, nil, &mdm.Checkin{Raw: body}, nil)
	}, nil
}
