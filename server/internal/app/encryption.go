package app

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/replycerts"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// openReplyCertificates opens retained reply-encryption identities when encrypted SQL
// storage is available.
func (a *App) openReplyCertificates(ctx context.Context) error {
	if a.db == nil || a.keyring == nil {
		return nil
	}
	if a.cfg.Storage == "sqlite" &&
		(strings.Contains(a.cfg.DSN, ":memory:") || strings.Contains(a.cfg.DSN, "mode=memory")) {
		return nil
	}
	st, err := statestore.Open(ctx, a.db, a.dialect, a.keyring)
	if err != nil {
		return fmt.Errorf("app: encryption certificate storage: %w", err)
	}
	a.ReplyCertificates = &replycerts.Manager{Store: st}
	return nil
}

// prepareCommandEncryption prepares retained encryption material for RotateFileVaultKey
// and leaves other command types unchanged.
func (a *App) prepareCommandEncryption(
	ctx context.Context,
	id mdm.EnrollmentID,
	cmd *mdm.Command,
) (*mdm.Command, error) {
	if cmd.RequestType != "RotateFileVaultKey" {
		return cmd, nil
	}
	if a.ReplyCertificates == nil {
		return nil, fmt.Errorf(
			"%w: FileVault encryption requires encrypted persistent SQL storage",
			ErrConfig,
		)
	}
	prepared, err := a.ReplyCertificates.Prepare(ctx, id, cmd)
	if err != nil {
		return nil, fmt.Errorf("app: prepare encryption certificate: %w", err)
	}
	return prepared, nil
}

// enqueueFileVaultEscrow persists an encryption identity before queueing the
// escrow profile. Certificate bytes and private keys are never client inputs.
func (a *App) enqueueFileVaultEscrow(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var input struct {
		CommandUUID       string
		ProfileIdentifier string
		Location          string
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
		return
	}
	if json.Unmarshal(body, &input) != nil || input.CommandUUID == "" ||
		input.ProfileIdentifier == "" ||
		input.Location == "" ||
		id.Channel != mdm.ChannelDevice {
		writeError(w, http.StatusBadRequest, replycerts.ErrInvalid)
		return
	}
	if a.ReplyCertificates == nil {
		writeError(w, http.StatusServiceUnavailable, ErrConfig)
		return
	}
	// Reject nonexistent or disabled enrollments before retaining key material.
	target, err := a.Store.Get(r.Context(), id)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	if !target.Enabled {
		a.storageStatus(w, r, storage.ErrDisabled)
		return
	}
	cmd, err := a.ReplyCertificates.EscrowProfile(
		r.Context(),
		id,
		input.CommandUUID,
		input.ProfileIdentifier,
		input.Location,
	)
	if err != nil {
		if errors.Is(err, replycerts.ErrInvalid) || errors.Is(err, replycerts.ErrConflict) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			a.storageStatus(w, r, err)
		}
		return
	}
	result, err := a.Core.Enqueue(
		r.Context(),
		[]mdm.EnrollmentID{id},
		cmd,
		storage.EnqueueOptions{},
	)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	out := map[string]any{"CommandUUID": cmd.UUID, "Queued": len(result.Queued)}
	if reason, ok := result.Skipped[id]; ok {
		out["Skipped"] = reason.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
