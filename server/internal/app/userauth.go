package app

import (
	"context"
	"encoding/hex"
	json "encoding/json/v2"
	"fmt"
	"os"

	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// userAuthenticator reads precomputed username:mdm:password HA1 hashes. A
// deployment can instead embed Config with its own verifier in future adapters.
func (a *App) userAuthenticator() (service.UserAuthenticateHandler, error) {
	file := a.cfg.Enroll.UserAuthHA1File
	if file == "" {
		return nil, nil //nolint:nilnil // Unconfigured optional integration has no handler.
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: user HA1 file: %w", ErrConfig, err)
	}
	var users map[string]string
	if err = json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("%w: invalid user HA1 JSON", ErrConfig)
	}
	for _, hash := range users {
		b, err := hex.DecodeString(hash)
		if err != nil || len(b) != 16 {
			return nil, fmt.Errorf("%w: invalid user HA1 digest", ErrConfig)
		}
	}
	d := &service.DigestUserAuth{
		Store: a.Store,
		Clock: a.cfg.Clock,
		Bus:   a.cfg.Bus,
		Verifier: service.HA1Verifier(
			func(_ context.Context, username, realm string) (string, error) {
				if realm != service.DefaultUserAuthRealm {
					return "", nil
				}
				return users[username], nil
			},
		),
	}
	return d.Handle, nil
}
