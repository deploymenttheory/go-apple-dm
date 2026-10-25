package lab

import (
	"context"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

type adeAPIAssertions struct {
	Authentication   string `json:"authentication"`
	AccountBinding   string `json:"account_binding"`
	FullFetch        string `json:"full_fetch"`
	DeltaSync        string `json:"delta_sync"`
	DesignatedDevice string `json:"designated_device"`
	ProfileReadback  string `json:"profile_readback"`
	DeltaChanges     bool   `json:"delta_changes_exercised"`
	Hardware         string `json:"hardware"`
}

// adePages exercises Apple's full-fetch and incremental cursors independently of
// assignment. It never resets the reference account's persisted cursor.
func (a *adeAccess) pages(ctx context.Context, s *Session, full bool, cursor string) (string, bool, error) {
	phase := "delta"
	if full {
		phase = "fetch"
	}
	seen := map[string]bool{}
	designated := false
	for number := 1; number <= 1000; number++ {
		var page *dep.DevicePage
		var err error
		if full {
			page, err = a.client.FetchDevices(ctx, a.config.Account, cursor, 2)
		} else {
			page, err = a.client.SyncDevices(ctx, a.config.Account, cursor, 2)
		}
		if err != nil {
			var remote *dep.Error
			if !full && errors.As(err, &remote) && remote.Code == dep.CodeExhaustedCursor {
				if saveErr := adeSave(s, "delta-exhausted.json", map[string]string{"code": dep.CodeExhaustedCursor}); saveErr != nil {
					return "", false, saveErr
				}
				return cursor, designated, nil
			}
			return "", false, adeError(ctx, phase+" traversal")
		}
		if page == nil || page.Cursor == "" || page.MoreToFollow && (page.Cursor == cursor || seen[page.Cursor]) {
			return "", false, adeError(ctx, phase+" continuation")
		}
		if err = adeSave(s, fmt.Sprintf("%s-page-%04d.json", phase, number), page); err != nil {
			return "", false, err
		}
		for _, device := range page.Devices {
			if inventory.CanonicalSerial(device.SerialNumber) == a.config.Serial {
				designated = true
			}
		}
		seen[page.Cursor] = true
		cursor = page.Cursor
		if !page.MoreToFollow {
			return cursor, designated, nil
		}
	}
	return "", false, adeError(ctx, phase+" page bound")
}

// liveADECollection proves read-only token/account, full/delta enumeration and
// designated device/profile readback through the shared SDK. No profile is defined
// or assigned, and the reference one-shot assignment endpoint is not invoked.
func liveADECollection(ctx context.Context, session *Session) (result error) {
	s, _, err := adeScope(session, "api")
	if err != nil {
		return err
	}
	assertions := adeAPIAssertions{
		Authentication: "unexercised", AccountBinding: "unexercised",
		FullFetch: "unexercised", DeltaSync: "unexercised", DesignatedDevice: "unexercised",
		ProfileReadback: "unexercised", Hardware: "unexercised",
	}
	defer func() { result = errors.Join(result, adeSave(s, "api-assertions.json", assertions)) }()
	a, err := adeOpen(ctx, s)
	if err != nil {
		return err
	}
	assertions.Authentication, assertions.AccountBinding = "passed", "passed"
	if err = adeSave(s, "account.json", a.detail); err != nil {
		return err
	}
	cursor, found, err := a.pages(ctx, s, true, "")
	if err != nil {
		return err
	}
	assertions.FullFetch = "passed"
	if !found {
		return fmt.Errorf("%w: populated designated ADE device unexercised", ErrBlocked)
	}
	_, changed, err := a.pages(ctx, s, false, cursor)
	if err != nil {
		return err
	}
	assertions.DeltaSync, assertions.DeltaChanges = "passed", changed
	device, err := a.device(ctx)
	if err != nil {
		return err
	}
	if err = adeSave(s, "device-detail.json", device); err != nil {
		return err
	}
	assertions.DesignatedDevice = "passed"
	profileUUID, err := profileState(device)
	if err != nil {
		return err
	}
	if profileUUID == "" {
		return nil // No assigned profile is an explicit unexercised profile readback.
	}
	profile, err := a.client.FetchProfile(ctx, a.config.Account, profileUUID)
	if err != nil {
		return adeError(ctx, "profile readback")
	}
	if profile == nil || profile.ProfileUUID != profileUUID {
		return adeError(ctx, "profile identity")
	}
	if err = adeSave(s, "observed-profile.json", profile); err != nil {
		return err
	}
	assertions.ProfileReadback = "passed"
	return nil
}
