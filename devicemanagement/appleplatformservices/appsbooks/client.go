package appsbooks

import (
	"context"
	"encoding/base64"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
)

// BaseURL is Apple's version 2 management API, distinct from catalog metadata.
const (
	BaseURL = "https://vpp.itunes.apple.com/mdm/v2"
	maxBody = 32 << 20
)

// Config defines one location client. Use one shared Client per location in a
// process; multiple processes must coordinate their aggregate request rate.
type Config struct {
	SToken string
	MDMID  string
	// UID optionally pins the expected library identifier before the first response.
	UID        string
	BaseURL    string
	HTTPClient *http.Client
	Clock      clock.Clock
	// ReadRetries permits this many GET retries for HTTP 429/5xx, maximum 5.
	// Zero disables retries. Mutations are never automatically replayed.
	ReadRetries int
}

// Client serializes calls and respects the discovered per-location request rate.
// Limits refresh on use after five minutes. No background goroutine is started.
type Client struct {
	token       string
	mdmID       string
	uid         string
	expires     time.Time
	base        *url.URL
	http        *http.Client
	clock       clock.Clock
	retries     int
	gate        chan struct{}
	service     ServiceConfiguration
	refreshed   time.Time
	nextRequest time.Time
}

// New validates a downloaded base64 sToken without contacting Apple. The
// original sToken, not its decoded token member, authenticates HTTP requests.
// Recreate the client when replacing a location token; no global cache is used.
func New(cfg Config) (*Client, error) {
	if cfg.MDMID == "" || cfg.ReadRetries < 0 || cfg.ReadRetries > 5 {
		return nil, ErrConfig
	}
	token := strings.TrimSpace(cfg.SToken)
	if len(token) > 1<<20 {
		return nil, ErrConfig
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid content token", ErrConfig)
	}
	var decoded struct {
		Token   string `json:"token"`
		ExpDate string `json:"expDate"`
		OrgName string `json:"orgName"`
	}
	if json.Unmarshal(data, &decoded) != nil || decoded.Token == "" {
		return nil, fmt.Errorf("%w: invalid content token", ErrConfig)
	}
	expires, err := time.Parse(time.RFC3339, decoded.ExpDate)
	if err != nil {
		expires, err = time.Parse("2006-01-02T15:04:05-0700", decoded.ExpDate)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: invalid token expiration", ErrConfig)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = BaseURL
	}
	base, err := httpsurl.Parse(cfg.BaseURL)
	if err != nil || base.RawQuery != "" {
		return nil, fmt.Errorf("%w: invalid base URL", ErrConfig)
	}
	base.Path = strings.TrimRight(base.Path, "/")
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if !cfg.Clock.Now().Before(expires) {
		return nil, ErrExpired
	}
	h := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		h = *cfg.HTTPClient
	}
	// Even same-host redirects must not replay a mutation or disclose bearer data.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		token:   token,
		mdmID:   cfg.MDMID,
		uid:     cfg.UID,
		expires: expires,
		base:    base,
		http:    &h,
		clock:   cfg.Clock,
		retries: cfg.ReadRetries,
		gate:    make(chan struct{}, 1),
	}, nil
}

// lock waits for exclusive client access, returning when the context is cancelled before
// acquisition.
func (c *Client) lock(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("appsbooks: waiting: %w", ctx.Err())
	}
}

// unlock releases the client gate after an operation completes.
func (c *Client) unlock() { <-c.gate }

// ServiceConfig returns a copy of configuration cached for at most five minutes.
func (c *Client) ServiceConfig(ctx context.Context) (ServiceConfiguration, error) {
	if err := c.lock(ctx); err != nil {
		return ServiceConfiguration{}, err
	}
	defer c.unlock()
	if err := c.refresh(ctx); err != nil {
		return ServiceConfiguration{}, err
	}
	out := c.service
	out.Limits = maps.Clone(out.Limits)
	out.URLs = maps.Clone(out.URLs)
	out.NotificationTypes = slices.Clone(out.NotificationTypes)
	out.ErrorCodes = slices.Clone(out.ErrorCodes)
	for i := range out.ErrorCodes {
		out.ErrorCodes[i].Info = slices.Clone(out.ErrorCodes[i].Info)
	}
	return out, nil
}

// refresh refreshes expired service configuration before resolving endpoint URLs and
// request limits.
func (c *Client) refresh(ctx context.Context) error {
	if !c.refreshed.IsZero() && c.clock.Since(c.refreshed) < 5*time.Minute {
		return nil
	}
	var config ServiceConfiguration
	u := *c.base
	u.Path += "/service/config"
	if err := c.request(ctx, http.MethodGet, &u, nil, &config, false); err != nil {
		return err
	}
	if config.Limits["maxRequestPerSecond"] <= 0 {
		return fmt.Errorf("%w: missing request rate", ErrProtocol)
	}
	c.service = config
	c.refreshed = c.clock.Now()
	return nil
}

// endpoint resolves a service endpoint name against the downloaded configuration.
func (c *Client) endpoint(name string) (*url.URL, error) {
	u, err := httpsurl.Parse(c.service.URLs[name])
	if err != nil || !strings.EqualFold(u.Host, c.base.Host) || u.RawQuery != "" {
		return nil, fmt.Errorf("%w: invalid discovered endpoint", ErrProtocol)
	}
	return u, nil
}

// call refreshes service configuration, resolves the named endpoint, and sends an
// authenticated request with the supplied query.
func (c *Client) call(ctx context.Context, name, method string, q url.Values, in, out any) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	u, err := c.endpoint(name)
	if err != nil {
		return err
	}
	u.RawQuery = q.Encode()
	return c.request(ctx, method, u, in, out, true)
}

// checkMeta rejects response metadata that violates the configured client ownership
// contract.
func (c *Client) checkMeta(meta ResponseMeta) error {
	if meta.UID == "" {
		return fmt.Errorf("%w: missing library identifier", ErrProtocol)
	}
	if c.uid != "" && meta.UID != c.uid {
		return ErrLocation
	}
	if meta.MDMInfo != nil && meta.MDMInfo.ID != c.mdmID {
		return ErrOwnership
	}
	c.uid = meta.UID
	return nil
}

// wait waits through the configured clock for the requested delay, returning cancellation
// instead of waiting further.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("appsbooks: waiting: %w", ctx.Err())
	case <-c.clock.After(d):
		return nil
	}
}
