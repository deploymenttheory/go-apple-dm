package publicappstoreidentity

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultURL            = "https://itunes.apple.com"
	DefaultLimit          = 50
	DefaultMaxBytes int64 = 8 << 20
	DefaultTimeout        = 15 * time.Second
)

// Operation errors support errors.Is. StatusError also exposes HTTP details.
var (
	ErrInvalid  = errors.New("publicappstoreidentity: invalid query or client configuration")
	ErrRequest  = errors.New("publicappstoreidentity: request failed")
	ErrStatus   = errors.New("publicappstoreidentity: unexpected HTTP status")
	ErrDecode   = errors.New("publicappstoreidentity: malformed response")
	ErrTooLarge = errors.New("publicappstoreidentity: response too large")
	ErrNotFound = errors.New("publicappstoreidentity: listing not found")
)

// Entity selects one of the documented software search entities.
type Entity string

const (
	Software     Entity = "software"
	IPadSoftware Entity = "iPadSoftware"
	MacSoftware  Entity = "macSoftware"
)

// Store selects a country storefront and a software entity. Neither is inferred.
type Store struct {
	Country string `json:"country"`
	Entity  Entity `json:"entity"`
}

// Query describes one search. Limit zero selects DefaultLimit; the maximum is 200.
type Query struct {
	Term  string `json:"term"`
	Store Store  `json:"store"`
	Limit int    `json:"limit"`
}

// App is a public store listing, with the storefront used to resolve it.
type App struct {
	ID        int64  `json:"id"`
	BundleID  string `json:"bundleID"`
	Name      string `json:"name"`
	Developer string `json:"developer"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Store     Store  `json:"store"`
}

// StatusError preserves the status and Retry-After header without retrying.
type StatusError struct {
	StatusCode int
	RetryAfter string
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: %d", ErrStatus, e.StatusCode) }
func (e *StatusError) Unwrap() error { return ErrStatus }

// Client is safe for concurrent use if its fields are not modified. Zero values
// select the documented defaults. HTTPClient may inject a transport or cache.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	MaxBytes   int64
	Timeout    time.Duration
}

// Search returns the matching listings. An empty result is successful. The API's
// limit bounds this single request; Search does not imply additional pages exist.
func (c *Client) Search(ctx context.Context, q Query) ([]App, error) {
	if strings.TrimSpace(q.Term) == "" {
		return nil, fmt.Errorf("%w: term is required", ErrInvalid)
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 1 || q.Limit > 200 {
		return nil, fmt.Errorf("%w: limit must be 1..200", ErrInvalid)
	}
	return c.request(ctx, "search", q.Store, url.Values{"term": {q.Term}, "limit": {strconv.Itoa(q.Limit)}})
}

// Lookup resolves one numeric App Store ID in the requested store. A missing
// listing returns ErrNotFound. Search terms are never substituted for an ID.
func (c *Client) Lookup(ctx context.Context, id int64, store Store) (App, error) {
	if id <= 0 {
		return App{}, fmt.Errorf("%w: positive store ID required", ErrInvalid)
	}
	apps, err := c.request(ctx, "lookup", store, url.Values{"id": {strconv.FormatInt(id, 10)}})
	if err != nil {
		return App{}, err
	}
	if len(apps) == 0 {
		return App{}, ErrNotFound
	}
	if len(apps) != 1 || apps[0].ID != id {
		return App{}, fmt.Errorf("%w: lookup ID mismatch", ErrDecode)
	}
	return apps[0], nil
}

func (c *Client) request(ctx context.Context, endpoint string, store Store, q url.Values) ([]App, error) {
	store.Country = strings.ToUpper(store.Country)
	if len(store.Country) != 2 || store.Country[0] < 'A' || store.Country[0] > 'Z' || store.Country[1] < 'A' || store.Country[1] > 'Z' {
		return nil, fmt.Errorf("%w: two-letter country required", ErrInvalid)
	}
	switch store.Entity {
	case Software, IPadSoftware, MacSoftware:
	default:
		return nil, fmt.Errorf("%w: software entity required", ErrInvalid)
	}
	base, maxBytes, timeout := c.BaseURL, c.MaxBytes, c.Timeout
	if base == "" {
		base = DefaultURL
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if maxBytes < 1 || maxBytes == math.MaxInt64 || timeout < 0 {
		return nil, ErrInvalid
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid base URL", ErrInvalid)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + endpoint
	q.Set("country", store.Country)
	q.Set("entity", string(store.Entity))
	q.Set("media", "software")
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	req.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequest, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	var response struct {
		Count   *int `json:"resultCount"`
		Results []struct {
			ID        int64  `json:"trackId"`
			BundleID  string `json:"bundleId"`
			Name      string `json:"trackName"`
			Developer string `json:"artistName"`
			Version   string `json:"version"`
			URL       string `json:"trackViewUrl"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	if response.Count == nil || *response.Count != len(response.Results) {
		return nil, fmt.Errorf("%w: result count mismatch", ErrDecode)
	}
	apps := make([]App, 0, len(response.Results))
	for _, r := range response.Results {
		if r.ID <= 0 || strings.TrimSpace(r.BundleID) == "" || strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("%w: incomplete listing", ErrDecode)
		}
		apps = append(apps, App{ID: r.ID, BundleID: r.BundleID, Name: r.Name, Developer: r.Developer, Version: r.Version, URL: r.URL, Store: store})
	}
	return apps, nil
}
