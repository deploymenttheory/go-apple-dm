package statestore

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// PublishCertificate participates in the state transaction, so the runtime APNs
// identity and workflow activation are visible at the same commit.
func PublishCertificate(ctx context.Context, tx state.Tx, identity lifecycle.Identity, material lifecycle.Material) error {
	if identity.Kind != lifecycle.Push {
		return nil
	}
	t, ok := tx.(*transaction)
	if !ok {
		return fmt.Errorf("statestore: certificate activation requires a SQL transaction")
	}
	if t.keyring == nil {
		return fmt.Errorf("statestore: certificate activation requires encryption")
	}
	_, err := sqlcommon.PutPushCertTx(ctx, t.q, t.d, t.keyring, identity.Topic, material.Certificate, material.Key, tx.Now())
	return err
}
