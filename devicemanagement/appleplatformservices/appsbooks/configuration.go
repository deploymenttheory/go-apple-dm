package appsbooks

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
)

// ClientConfig reads configuration and checks MDM and library identity.
func (c *Client) ClientConfig(ctx context.Context) (ClientConfiguration, error) {
	if err := c.lock(ctx); err != nil {
		return ClientConfiguration{}, err
	}
	defer c.unlock()
	return c.clientConfig(ctx)
}

func (c *Client) clientConfig(ctx context.Context) (ClientConfiguration, error) {
	var out ClientConfiguration
	err := c.call(ctx, "clientConfig", http.MethodGet, nil, nil, &out)
	return out, err
}

// SetClientConfig explicitly claims an unclaimed location or updates this MDM's
// configuration. It reads ownership first and never overwrites another MDM ID.
// Apple does not offer compare-and-swap: administrative coordination is still
// required if multiple MDMs configure the same location concurrently.
func (c *Client) SetClientConfig(
	ctx context.Context,
	in ClientConfigurationRequest,
) (ClientConfiguration, error) {
	if err := c.lock(ctx); err != nil {
		return ClientConfiguration{}, err
	}
	defer c.unlock()
	if _, err := c.clientConfig(ctx); err != nil {
		return ClientConfiguration{}, err
	}
	if err := c.validateConfig(in); err != nil {
		return ClientConfiguration{}, err
	}
	var out ClientConfiguration
	err := c.call(ctx, "clientConfig", http.MethodPost, nil, in, &out)
	return out, err
}

func (c *Client) validateConfig(in ClientConfigurationRequest) error {
	if in.MDMInfo.ID != c.mdmID || in.MDMInfo.Name == "" || in.MDMInfo.Metadata == "" {
		return ErrInput
	}
	for key, value := range map[string]string{"maxMdmIdLength": in.MDMInfo.ID, "maxMdmNameLength": in.MDMInfo.Name, "maxMdmMetadataLength": in.MDMInfo.Metadata, "maxNotificationLength": in.NotificationURL} {
		if err := c.limit(key, utf8.RuneCountInString(value)); err != nil {
			return err
		}
	}
	if err := c.limit(
		"maxNotificationLength",
		utf8.RuneCountInString(in.NotificationAuthToken),
	); err != nil {
		return err
	}
	if (in.NotificationURL == "") != (in.NotificationAuthToken == "") {
		return ErrInput
	}
	if in.NotificationURL == "" && len(in.NotificationTypes) != 0 {
		return ErrInput
	}
	if in.NotificationURL != "" {
		u, err := httpsurl.Parse(in.NotificationURL)
		if err != nil || u.RawQuery != "" ||
			strings.ContainsAny(in.NotificationAuthToken, " \t\r\n") {
			return ErrInput
		}
	}
	for _, kind := range in.NotificationTypes {
		switch kind {
		case "TEST_NOTIFICATION",
			"ASSET_COUNT",
			"ASSET_MANAGEMENT",
			"USER_MANAGEMENT",
			"USER_ASSOCIATED":
		default:
			return fmt.Errorf("%w: unsupported notification type", ErrInput)
		}
	}
	return nil
}

// InvitationURL fills Apple's current invitation template with an inviteCode.
// Send it through your own user communication flow; this method sends no email.
func (c *Client) InvitationURL(ctx context.Context, inviteCode string) (string, error) {
	if inviteCode == "" {
		return "", ErrInput
	}
	config, err := c.ServiceConfig(ctx)
	if err != nil {
		return "", err
	}
	template := config.URLs["invitationEmail"]
	if !strings.Contains(template, "%25inviteCode%25") {
		return "", ErrProtocol
	}
	link := strings.ReplaceAll(template, "%25inviteCode%25", url.QueryEscape(inviteCode))
	if _, err := httpsurl.Parse(link); err != nil {
		return "", ErrProtocol
	}
	return link, nil
}

func (c *Client) limit(key string, n int) error {
	limit := c.service.Limits[key]
	if limit <= 0 {
		return fmt.Errorf("%w: missing %s", ErrProtocol, key)
	}
	if n > limit {
		return fmt.Errorf("%w: %s", ErrLimit, key)
	}
	return nil
}

func (c *Client) requireOwner(ctx context.Context) error {
	config, err := c.clientConfig(ctx)
	if err != nil {
		return err
	}
	if config.MDMInfo == nil {
		return ErrOwnership
	}
	return nil
}
