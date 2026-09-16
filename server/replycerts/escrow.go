package replycerts

import (
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
)

// EscrowProfile prepares an InstallProfile command containing a system-scoped
// FileVault recovery-key escrow payload and its automatically generated RSA
// certificate. Retain commandUUID to retrieve the matching Recipient. Repeating
// the same request preserves the certificate and every payload UUID.
//
// On macOS 26, installing this profile can rotate an existing personal recovery
// key automatically when a bootstrap token is present. The caller must establish
// those prerequisites, check for an existing escrow profile, and arrange secure
// retrieval through SecurityInfo. Removing the profile does not restore a prior
// recovery key. Keep this binding while the profile or its encrypted keys exist.
// https://support.apple.com/en-us/124963
// https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow
func (m *Manager) EscrowProfile(
	ctx context.Context,
	id mdm.EnrollmentID,
	commandUUID, identifier, location string,
) (*mdm.Command, error) {
	if m.Store == nil || id.Channel != mdm.ChannelDevice || id.ID == "" || commandUUID == "" ||
		identifier == "" ||
		location == "" {
		return nil, ErrInvalid
	}
	data, err := json.Marshal(
		struct{ Kind, Identifier, Location string }{"filevault-escrow", identifier, location},
	)
	if err != nil {
		return nil, fmt.Errorf("replycerts: escrow metadata: %w", err)
	}
	selected, err := m.selectIdentity(ctx, id, commandUUID, sha256.Sum256(data), nil, true)
	if err != nil {
		return nil, err
	}
	cert, _, err := material(selected)
	if err != nil {
		return nil, err
	}
	if selected.ProfileUUID == "" || selected.CertificateUUID == "" || selected.EscrowUUID == "" {
		return nil, ErrInvalid
	}
	p := profile.Profile{
		Identifier:  identifier,
		UUID:        selected.ProfileUUID,
		Scope:       profile.ScopeSystem,
		DisplayName: "FileVault recovery key escrow",
		Payloads: []profile.Payload{
			{
				Identifier: identifier + ".certificate",
				UUID:       selected.CertificateUUID,
				Content:    &profiles.CertificatePKCS1{PayloadContent: cert.Raw},
			},
			{
				Identifier: identifier + ".escrow",
				UUID:       selected.EscrowUUID,
				Content: &profiles.FDERecoveryKeyEscrow{
					Location:               location,
					EncryptCertPayloadUUID: selected.CertificateUUID,
				},
			},
		},
	}
	payload, err := p.Marshal()
	if err != nil {
		return nil, fmt.Errorf("replycerts: escrow profile: %w", err)
	}
	cmd, err := mdm.NewCommand(
		&commands.InstallProfile{Payload: payload},
		mdm.WithUUID(commandUUID),
	)
	if err != nil {
		return nil, fmt.Errorf("replycerts: escrow command: %w", err)
	}
	return cmd, nil
}
