package gdmf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"
)

// DefaultURL is Apple's software lookup service.
const DefaultURL = "https://gdmf.apple.com/v2/pmv"

// Defaults for Client.
const (
	DefaultTTL      = time.Hour
	DefaultMaxBytes = 8 << 20
	DefaultTimeout  = 15 * time.Second
)

// Errors returned by this package.
var (
	ErrRequest  = errors.New("gdmf: request failed")
	ErrStatus   = errors.New("gdmf: unexpected status")
	ErrDecode   = errors.New("gdmf: malformed catalog")
	ErrTooLarge = errors.New("gdmf: catalog too large")
	ErrNotFound = errors.New("gdmf: no asset for device")
)

// Asset is one operating system release in the catalog.
//
//nolint:tagliatelle // Apple's key names
type Asset struct {
	ProductVersion   string   `json:"ProductVersion"`
	Build            string   `json:"Build"`
	PostingDate      string   `json:"PostingDate"`
	ExpirationDate   string   `json:"ExpirationDate"`
	SupportedDevices []string `json:"SupportedDevices"`
}

// Catalog is the pmv document: asset sets keyed by platform ("iOS",
// "macOS", "visionOS", ...). PublicAssetSets holds released versions,
// AssetSets adds versions still in seeding.
//
//nolint:tagliatelle // Apple's key names
type Catalog struct {
	PublicAssetSets map[string][]Asset `json:"PublicAssetSets"`
	AssetSets       map[string][]Asset `json:"AssetSets"`
}

// Latest returns the newest asset whose SupportedDevices lists deviceID.
// Only public sets are searched unless includeNonPublic is set. Newest
// means the highest ProductVersion, then the later PostingDate.
func (c *Catalog) Latest(deviceID string, includeNonPublic bool) (*Asset, bool) {
	if c == nil || deviceID == "" {
		return nil, false
	}
	sets := []map[string][]Asset{c.PublicAssetSets}
	if includeNonPublic {
		sets = append(sets, c.AssetSets)
	}
	var best *Asset
	for _, set := range sets {
		for _, assets := range set {
			for i := range assets {
				a := &assets[i]
				if !supports(a, deviceID) {
					continue
				}
				if best == nil || newer(a, best) {
					best = a
				}
			}
		}
	}
	if best == nil {
		return nil, false
	}
	out := *best
	out.SupportedDevices = append([]string(nil), best.SupportedDevices...)
	return &out, true
}

func supports(a *Asset, deviceID string) bool {
	for _, d := range a.SupportedDevices {
		if d == deviceID {
			return true
		}
	}
	return false
}

func newer(a, b *Asset) bool {
	if c := CompareVersions(a.ProductVersion, b.ProductVersion); c != 0 {
		return c > 0
	}
	return a.PostingDate > b.PostingDate
}

// CompareVersions orders dotted numeric versions ("17.5.1" < "18.0").
// A missing component counts as zero; a supplemental suffix after a space
// ("16.1 (a)") is ignored; non-numeric components compare as strings.
func CompareVersions(a, b string) int {
	pa, pb := components(a), components(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := compareComponent(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func components(v string) []string {
	v, _, _ = strings.Cut(strings.TrimSpace(v), " ")
	if v == "" {
		return nil
	}
	return strings.Split(v, ".")
}

func compareComponent(x, y string) int {
	nx, ex := strconv.Atoi(x)
	ny, ey := strconv.Atoi(y)
	if x == "" {
		nx, ex = 0, nil
	}
	if y == "" {
		ny, ey = 0, nil
	}
	if ex == nil && ey == nil {
		switch {
		case nx < ny:
			return -1
		case nx > ny:
			return 1
		}
		return 0
	}
	return strings.Compare(x, y)
}

// Lookup answers the latest version for a device model. Implementations
// are Client and gdmftest.Fake.
type Lookup interface {
	// Latest returns the newest asset for deviceID (a PRODUCT or
	// SOFTWARE_UPDATE_DEVICE_ID value such as "iPhone15,2"). ErrNotFound
	// means the catalog was read but lists nothing for the device.
	Latest(ctx context.Context, deviceID string) (*Asset, error)
}

// Client fetches and caches the catalog. The zero value is usable; fields
// are read at first use and must not change afterwards.
type Client struct {
	// URL defaults to DefaultURL.
	URL string
	// HTTP defaults to a client with DefaultTimeout.
	HTTP *http.Client
	// TTL bounds how long a catalog is served before a refresh; default
	// DefaultTTL. When a refresh fails the previous catalog is served and
	// the error is returned by Refresh only.
	TTL time.Duration
	// MaxBytes bounds the response body; default DefaultMaxBytes.
	MaxBytes int64
	// Now defaults to time.Now.
	Now func() time.Time
	// IncludeNonPublic searches AssetSets (seeding versions) as well as
	// PublicAssetSets.
	IncludeNonPublic bool
	// UserAgent is sent with the request when set.
	UserAgent string

	mu      sync.Mutex
	catalog *Catalog
	fetched time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

// Catalog returns the cached catalog, refreshing it when older than TTL.
// A failed refresh returns the stale catalog when one exists.
func (c *Client) Catalog(ctx context.Context) (*Catalog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog != nil && c.now().Sub(c.fetched) < c.ttl() {
		return c.catalog, nil
	}
	cat, err := c.fetch(ctx)
	if err != nil {
		if c.catalog != nil {
			return c.catalog, nil
		}
		return nil, err
	}
	c.catalog, c.fetched = cat, c.now()
	return cat, nil
}

// Refresh fetches the catalog unconditionally and reports any failure.
func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cat, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	c.catalog, c.fetched = cat, c.now()
	return nil
}

// Latest implements Lookup.
func (c *Client) Latest(ctx context.Context, deviceID string) (*Asset, error) {
	cat, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	a, ok := cat.Latest(deviceID, c.IncludeNonPublic)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, deviceID)
	}
	return a, nil
}

func (c *Client) fetch(ctx context.Context) (*Catalog, error) {
	u := c.URL
	if u == "" {
		u = DefaultURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", ErrStatus, resp.StatusCode)
	}
	limit := c.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, limit)
	}
	var cat Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	if cat.PublicAssetSets == nil && cat.AssetSets == nil {
		return nil, fmt.Errorf("%w: no asset sets", ErrDecode)
	}
	return &cat, nil
}
