package appsbooks

import (
	"context"
	"net/http"
	"net/url"
)

// Associate schedules licensing. An EventID is not an installed/assigned result.
func (c *Client) Associate(ctx context.Context, in ManageAssetsRequest) (Event, error) {
	return c.manageAssets(ctx, "associateAssets", in)
}

// Disassociate schedules removal of revocable assignments; books are not revocable.
func (c *Client) Disassociate(ctx context.Context, in ManageAssetsRequest) (Event, error) {
	return c.manageAssets(ctx, "disassociateAssets", in)
}

// manageAssets submits an asset assignment or revocation batch after validating identifiers
// and service limits.
func (c *Client) manageAssets(
	ctx context.Context,
	endpoint string,
	in ManageAssetsRequest,
) (Event, error) {
	if err := c.lock(ctx); err != nil {
		return Event{}, err
	}
	defer c.unlock()
	if err := c.requireOwner(ctx); err != nil {
		return Event{}, err
	}
	if len(in.Assets) == 0 {
		return Event{}, ErrInput
	}
	if err := c.limit("maxAssets", len(in.Assets)); err != nil {
		return Event{}, err
	}
	seen := map[Asset]bool{}
	for _, a := range in.Assets {
		if a.AdamID == "" || (a.PricingParam != "STDQ" && a.PricingParam != "PLUS") || seen[a] {
			return Event{}, ErrInput
		}
		seen[a] = true
	}
	if err := c.targets(
		in.ClientUserIDs,
		in.SerialNumbers,
		"maxClientUserIds",
		"maxSerialNumbers",
	); err != nil {
		return Event{}, err
	}
	return c.event(ctx, endpoint, in)
}

// Revoke schedules revocation of all revocable assignments from these targets.
func (c *Client) Revoke(ctx context.Context, in RevokeAssetsRequest) (Event, error) {
	if err := c.lock(ctx); err != nil {
		return Event{}, err
	}
	defer c.unlock()
	if err := c.requireOwner(ctx); err != nil {
		return Event{}, err
	}
	if err := c.targets(
		in.ClientUserIDs,
		in.SerialNumbers,
		"maxRevokeClientUserIds",
		"maxRevokeSerialNumbers",
	); err != nil {
		return Event{}, err
	}
	return c.event(ctx, "revokeAssets", in)
}

// targets validates and counts the device and user targets in an asset-management batch.
func (c *Client) targets(users, devices []string, userLimit, deviceLimit string) error {
	if len(users)+len(devices) == 0 || !unique(users) || !unique(devices) {
		return ErrInput
	}
	if err := c.limit(userLimit, len(users)); err != nil {
		return err
	}
	return c.limit(deviceLimit, len(devices))
}

// unique rejects empty or repeated identifiers in a submitted batch.
func unique(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// CreateUsers registers users; registration alone does not associate an Apple Account.
func (c *Client) CreateUsers(ctx context.Context, in ManageUsersRequest) (Event, error) {
	return c.manageUsers(ctx, "createUsers", in)
}

// UpdateUsers schedules updates to users' email/Managed Apple Account information.
func (c *Client) UpdateUsers(ctx context.Context, in ManageUsersRequest) (Event, error) {
	return c.manageUsers(ctx, "updateUsers", in)
}

// RetireUsers retires users; previously assigned books cannot be reclaimed.
func (c *Client) RetireUsers(ctx context.Context, in ManageUsersRequest) (Event, error) {
	return c.manageUsers(ctx, "retireUsers", in)
}

// manageUsers submits a user-management batch after validating user IDs and the service
// batch limit.
func (c *Client) manageUsers(
	ctx context.Context,
	endpoint string,
	in ManageUsersRequest,
) (Event, error) {
	if err := c.lock(ctx); err != nil {
		return Event{}, err
	}
	defer c.unlock()
	if err := c.requireOwner(ctx); err != nil {
		return Event{}, err
	}
	if len(in.Users) == 0 {
		return Event{}, ErrInput
	}
	if err := c.limit("maxUsers", len(in.Users)); err != nil {
		return Event{}, err
	}
	ids := make([]string, len(in.Users))
	for i, u := range in.Users {
		ids[i] = u.ClientUserID
	}
	if !unique(ids) {
		return Event{}, ErrInput
	}
	return c.event(ctx, endpoint, in)
}

// event submits an asynchronous management request and requires a nonempty event ID in its
// response.
func (c *Client) event(ctx context.Context, endpoint string, in any) (Event, error) {
	var out Event
	err := c.call(ctx, endpoint, http.MethodPost, nil, in, &out)
	if err == nil && out.EventID == "" {
		err = ErrProtocol
	}
	return out, err
}

// EventStatus reads aggregate progress. After a missing notification, Apple
// recommends checking after five minutes and polling PENDING at >=30s intervals.
// The caller owns that schedule, persistence and any deliberate resubmission.
func (c *Client) EventStatus(ctx context.Context, eventID string) (Status, error) {
	if eventID == "" {
		return Status{}, ErrInput
	}
	if err := c.lock(ctx); err != nil {
		return Status{}, err
	}
	defer c.unlock()
	var out Status
	err := c.call(ctx, "eventStatus", http.MethodGet, url.Values{"eventId": {eventID}}, nil, &out)
	if err == nil &&
		(out.EventStatus == "" || out.NumRequested < 0 || out.NumCompleted < 0 || out.NumCompleted > out.NumRequested) {
		err = ErrProtocol
	}
	return out, err
}

// CheckAssignment applies the asset's reported capability flags. Books require
// user assignment and cannot be reclaimed. The server remains authoritative.
func (a AssetRecord) CheckAssignment(device, removing bool) error {
	if (a.ProductType != "App" && a.ProductType != "Book") ||
		(device && (!a.DeviceAssignable || a.ProductType == "Book")) ||
		(removing && (!a.Revocable || a.ProductType == "Book")) {
		return ErrInput
	}
	return nil
}
