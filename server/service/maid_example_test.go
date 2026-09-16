package service_test

import (
	"context"
	"crypto"
	"crypto/x509"
	"time"

	enrollprotocol "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// maidConfiguration receives the ADE server's registered certificate/key and
// server_uuid from DEP AccountDetail. It is independent of AXM ES256 credentials.
func maidConfiguration(cert *x509.Certificate, key crypto.Signer, serverUUID string) service.Config {
	return service.Config{GetToken: func(_ context.Context, _ *mdm.Request, m *checkin.GetToken) (*checkin.GetTokenResponse, error) {
		if m.TokenServiceType != enrollprotocol.MAIDService {
			return nil, &service.Error{Code: service.CodeBadRequest, Err: service.ErrInvalidMessage}
		}
		token, err := enrollprotocol.MAIDToken(cert, key, serverUUID, time.Now())
		if err != nil {
			return nil, err
		}
		return &checkin.GetTokenResponse{TokenData: token}, nil
	}}
}

func ExampleConfig_managedAppleAccount() {
	// Add this capability to the fully configured enrollment MDM payload.
	payload := profiles.MDM{ServerCapabilities: []string{"com.apple.mdm.token"}}
	_ = payload
	// Pass maidConfiguration's GetToken handler into service.New along with the
	// existing store and identity policy. Service authenticates the check-in
	// before invoking the handler; never expose an unauthenticated JWT endpoint.
	_ = maidConfiguration
}
