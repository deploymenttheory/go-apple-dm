package axm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Workflow constants.
const (
	// MaxMigrationDeadline is how far ahead a migration deadline may be.
	MaxMigrationDeadline = 90 * 24 * time.Hour
	// DefaultWaitInterval and DefaultWaitTimeout are WaitForActivity's
	// defaults; Apple's activities take minutes.
	DefaultWaitInterval = 5 * time.Second
	DefaultWaitTimeout  = 10 * time.Minute
	// DefaultMaxWaitInterval caps the backoff of WaitForActivity.
	DefaultMaxWaitInterval = time.Minute
	// AssignmentPollInterval is how often WaitForAssignedServer looks; the
	// linkage lags the activity by up to about 15 seconds.
	AssignmentPollInterval = 2 * time.Second
)

// NewActivityRequest builds the JSON:API body for an activity with
// Apple's rules checked: at least one serial; a server for ASSIGN_DEVICES
// and ASSIGN_DEVICES_WITH_MDM_MIGRATION_DEADLINE, none for the other
// types; a deadline within MaxMigrationDeadline of now for the two
// deadline activities and none for the others.
func NewActivityRequest(typ OrgDeviceActivityType, serverID string, serials []string, deadline, now time.Time) (OrgDeviceActivityCreateRequest, error) {
	var req OrgDeviceActivityCreateRequest
	if len(serials) == 0 {
		return req, fmt.Errorf("%w: at least one serial number is required", ErrActivityRule)
	}
	for _, s := range serials {
		if s == "" {
			return req, fmt.Errorf("%w: empty serial number", ErrActivityRule)
		}
	}
	needsServer, needsDeadline := false, false
	switch typ {
	case ActivityAssignDevices:
		needsServer = true
	case ActivityAssignDevicesWithMigrationDeadline:
		needsServer, needsDeadline = true, true
	case ActivityUpdateMigrationDeadline:
		needsDeadline = true
	case ActivityUnassignDevices, ActivityCancelMigration, ActivityReleaseDevices:
	default:
		return req, fmt.Errorf("%w: unknown activity type %q", ErrActivityRule, typ)
	}
	switch {
	case needsServer && serverID == "":
		return req, fmt.Errorf("%w: %s requires a device management service id", ErrActivityRule, typ)
	case !needsServer && serverID != "":
		return req, fmt.Errorf("%w: %s takes no device management service", ErrActivityRule, typ)
	}
	switch {
	case needsDeadline && deadline.IsZero():
		return req, fmt.Errorf("%w: %s requires a migration deadline", ErrActivityRule, typ)
	case needsDeadline && deadline.After(now.Add(MaxMigrationDeadline)):
		return req, fmt.Errorf("%w: migration deadline %s is more than 90 days ahead", ErrActivityRule, deadline.Format(time.RFC3339))
	case !needsDeadline && !deadline.IsZero():
		return req, fmt.Errorf("%w: %s takes no migration deadline", ErrActivityRule, typ)
	}
	req.Data.Type = TypeOrgDeviceActivities
	req.Data.Attributes.ActivityType = typ
	if needsDeadline {
		req.Data.Attributes.ActivityTypeMetadata = &ActivityTypeMetadata{MDMMigrationDeadlineDateTime: deadline.UTC()}
	}
	if needsServer {
		req.Data.Relationships.MDMServer = &SingleLinkage{Data: Linkage{Type: TypeMDMServers, ID: serverID}}
	}
	req.Data.Relationships.Devices.Data = make([]Linkage, len(serials))
	for i, s := range serials {
		req.Data.Relationships.Devices.Data[i] = Linkage{Type: TypeOrgDevices, ID: s}
	}
	return req, nil
}

// activity builds and submits one activity.
func (c *Client) activity(ctx context.Context, typ OrgDeviceActivityType, serverID string, serials []string, deadline time.Time) (*OrgDeviceActivity, error) {
	req, err := NewActivityRequest(typ, serverID, serials, deadline, c.clock.Now())
	if err != nil {
		return nil, err
	}
	return c.CreateOrgDeviceActivity(ctx, req)
}

// AssignDevices assigns serials to the device management service serverID.
func (c *Client) AssignDevices(ctx context.Context, serverID string, serials []string) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityAssignDevices, serverID, serials, time.Time{})
}

// UnassignDevices removes serials from their device management service.
func (c *Client) UnassignDevices(ctx context.Context, serials []string) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityUnassignDevices, "", serials, time.Time{})
}

// ReleaseDevices releases serials from the organization (Apple Business
// only). Released devices lose their enrollment assignment and blueprints.
func (c *Client) ReleaseDevices(ctx context.Context, serials []string) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityReleaseDevices, "", serials, time.Time{})
}

// AssignWithMigrationDeadline assigns serials to serverID and schedules a
// migration to complete by deadline (at most 90 days ahead).
func (c *Client) AssignWithMigrationDeadline(ctx context.Context, serverID string, serials []string, deadline time.Time) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityAssignDevicesWithMigrationDeadline, serverID, serials, deadline)
}

// UpdateMigrationDeadline moves the deadline of an in-progress migration.
func (c *Client) UpdateMigrationDeadline(ctx context.Context, serials []string, deadline time.Time) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityUpdateMigrationDeadline, "", serials, deadline)
}

// CancelMigration cancels an in-progress migration.
func (c *Client) CancelMigration(ctx context.Context, serials []string) (*OrgDeviceActivity, error) {
	return c.activity(ctx, ActivityCancelMigration, "", serials, time.Time{})
}

// WaitOptions tune WaitForActivity.
type WaitOptions struct {
	// Interval is the first polling interval; default DefaultWaitInterval.
	Interval time.Duration
	// Timeout bounds the whole wait; default DefaultWaitTimeout.
	Timeout time.Duration
	// Backoff multiplies the interval after every poll; values at or below
	// 1 keep it constant. MaxInterval caps it; default
	// DefaultMaxWaitInterval.
	Backoff     float64
	MaxInterval time.Duration
}

func (o WaitOptions) defaults() WaitOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultWaitInterval
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultWaitTimeout
	}
	if o.Backoff < 1 {
		o.Backoff = 1
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = DefaultMaxWaitInterval
	}
	if o.MaxInterval < o.Interval {
		o.MaxInterval = o.Interval
	}
	return o
}

// WaitForActivity polls the activity until it reaches a terminal status
// (COMPLETED, STOPPED, or FAILED) and returns it; the caller checks
// Succeeded. ErrWaitTimeout is returned when the timeout elapses first, a
// context error when the context ends, and any API error at once.
func (c *Client) WaitForActivity(ctx context.Context, id string, o WaitOptions) (*OrgDeviceActivity, error) {
	if err := requireID("activity id", id); err != nil {
		return nil, err
	}
	o = o.defaults()
	deadline := c.clock.Now().Add(o.Timeout)
	interval := o.Interval
	for {
		act, err := c.GetOrgDeviceActivity(ctx, id, GetOptions{})
		if err != nil {
			return nil, err
		}
		if act.Terminal() {
			return act, nil
		}
		now := c.clock.Now()
		if !now.Before(deadline) {
			return act, fmt.Errorf("%w: activity %s still %s/%s after %s", ErrWaitTimeout, id, act.Attributes.Status, act.Attributes.SubStatus, o.Timeout)
		}
		if err := c.sleep(ctx, min(interval, deadline.Sub(now))); err != nil {
			return act, err
		}
		interval = min(time.Duration(float64(interval)*o.Backoff), o.MaxInterval)
	}
}

// sleep waits d on the clock or until the context ends.
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-c.clock.After(d):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrTransport, ctx.Err())
	}
}

// FetchActivityLog streams the CSV at a completed activity's downloadUrl.
// The URL must be on the API host; the bearer token is sent with it. The
// caller closes the reader.
func (c *Client) FetchActivityLog(ctx context.Context, downloadURL string) (io.ReadCloser, error) {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("%w: download URL: %w", ErrArgument, err)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return nil, fmt.Errorf("%w: %s", ErrForeignHost, downloadURL)
	}
	resp, err := c.roundTrip(ctx, request{method: http.MethodGet, rawURL: u.String(), accept: "text/csv, application/json"})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// WaitForAssignedServer polls a device's assignedServer linkage until it
// names serverID (or, for an empty serverID, until it is empty), then
// returns nil. While Apple catches up the linkage may carry an empty id or
// answer 404; both are tolerated until timeout, which yields
// ErrWaitTimeout.
func (c *Client) WaitForAssignedServer(ctx context.Context, serial, serverID string, timeout time.Duration) error {
	if err := requireID("serial number", serial); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := c.clock.Now().Add(timeout)
	for {
		link, err := c.GetAssignedServerLinkage(ctx, serial)
		switch {
		case err != nil && !IsNotFound(err):
			return err
		case err == nil && link.ID == serverID:
			return nil
		case IsNotFound(err) && serverID == "":
			return nil
		}
		now := c.clock.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("%w: %s not assigned to %q after %s", ErrWaitTimeout, serial, serverID, timeout)
		}
		if err := c.sleep(ctx, min(AssignmentPollInterval, deadline.Sub(now))); err != nil {
			return err
		}
	}
}
