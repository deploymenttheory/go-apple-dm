package appsbooks

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// AssetsQuery filters asset counts. Assets do not accept SinceVersionID.
type AssetsQuery struct {
	PageIndex         int
	AdamID            string
	ProductType       string
	PricingParam      string
	Revocable         *bool
	DeviceAssignable  *bool
	MinAvailableCount *int64
	MaxAvailableCount *int64
	MinAssignedCount  *int64
	MaxAssignedCount  *int64
}

// AssignmentsQuery supports incremental reads while retaining target filters.
type AssignmentsQuery struct {
	PageIndex            int
	SinceVersionID       string
	AdamID               string
	PricingParam         string
	ClientUserID         string
	SerialNumber         string
	ExcludeInactiveUsers *bool
	IncludeUserState     *bool
}

// UsersQuery filters active, retired or modified users.
type UsersQuery struct {
	PageIndex      int
	SinceVersionID string
	ClientUserID   string
	ActiveOnly     *bool
	RetiredOnly    *bool
}

// Assets retrieves one page of app/book counts.
func (c *Client) Assets(ctx context.Context, q AssetsQuery) (AssetsPage, error) {
	v := url.Values{"pageIndex": {strconv.Itoa(q.PageIndex)}}
	setString(v, "adamId", q.AdamID)
	setString(v, "productType", q.ProductType)
	setString(v, "pricingParam", q.PricingParam)
	setBool(v, "revocable", q.Revocable)
	setBool(v, "deviceAssignable", q.DeviceAssignable)
	for key, value := range map[string]*int64{"minAvailableCount": q.MinAvailableCount, "maxAvailableCount": q.MaxAvailableCount, "minAssignedCount": q.MinAssignedCount, "maxAssignedCount": q.MaxAssignedCount} {
		if value != nil {
			if *value < 0 {
				return AssetsPage{}, ErrInput
			}
			v.Set(key, strconv.FormatInt(*value, 10))
		}
	}
	out, err := readPage[AssetsPage](ctx, c, "getAssets", v, q.PageIndex)
	if err == nil {
		err = checkPage(out.Pagination, q.PageIndex)
	}
	return out, err
}

// Assignments retrieves one page of full or incremental assignments.
func (c *Client) Assignments(ctx context.Context, q AssignmentsQuery) (AssignmentsPage, error) {
	v := url.Values{"pageIndex": {strconv.Itoa(q.PageIndex)}}
	setString(v, "sinceVersionId", q.SinceVersionID)
	setString(v, "adamId", q.AdamID)
	setString(v, "pricingParam", q.PricingParam)
	setString(v, "clientUserId", q.ClientUserID)
	setString(v, "serialNumber", q.SerialNumber)
	setBool(v, "excludeInactiveUsers", q.ExcludeInactiveUsers)
	setBool(v, "includeUserState", q.IncludeUserState)
	out, err := readPage[AssignmentsPage](ctx, c, "getAssignments", v, q.PageIndex)
	if err == nil {
		err = checkPage(out.Pagination, q.PageIndex)
	}
	return out, err
}

// Users retrieves one page of full or incremental user state.
func (c *Client) Users(ctx context.Context, q UsersQuery) (UsersPage, error) {
	v := url.Values{"pageIndex": {strconv.Itoa(q.PageIndex)}}
	setString(v, "sinceVersionId", q.SinceVersionID)
	setString(v, "clientUserId", q.ClientUserID)
	setBool(v, "activeOnly", q.ActiveOnly)
	setBool(v, "retiredOnly", q.RetiredOnly)
	if q.ActiveOnly != nil && q.RetiredOnly != nil && *q.ActiveOnly && *q.RetiredOnly {
		return UsersPage{}, ErrInput
	}
	out, err := readPage[UsersPage](ctx, c, "getUsers", v, q.PageIndex)
	if err == nil {
		err = checkPage(out.Pagination, q.PageIndex)
	}
	return out, err
}

// readPage decodes page data and pagination metadata from an Apps and Books response.
func readPage[T any](
	ctx context.Context,
	c *Client,
	endpoint string,
	q url.Values,
	index int,
) (T, error) {
	var out T
	if index < 0 {
		return out, ErrInput
	}
	if err := c.lock(ctx); err != nil {
		return out, err
	}
	defer c.unlock()
	err := c.call(ctx, endpoint, http.MethodGet, q, nil, &out)
	return out, err
}

// checkPage validates pagination state before following the next service page.
func checkPage(p Pagination, index int) error {
	if p.CurrentPageIndex != index || p.TotalPages < 0 || p.Size < 0 ||
		(p.NextPageIndex != nil && *p.NextPageIndex <= index) {
		return fmt.Errorf("%w: invalid page progression", ErrProtocol)
	}
	return nil
}

// setString adds an optional string query parameter when its value is present.
func setString(v url.Values, key, value string) {
	if value != "" {
		v.Set(key, value)
	}
}

// setBool adds an optional boolean query parameter without conflating false with absence.
func setBool(v url.Values, key string, value *bool) {
	if value != nil {
		v.Set(key, strconv.FormatBool(*value))
	}
}

// WalkAssets visits all pages sequentially, returning the first page's version
// only on success. maxPages must be positive and bounds a changing result set.
func (c *Client) WalkAssets(
	ctx context.Context,
	q AssetsQuery,
	maxPages int,
	visit func(AssetRecord) error,
) (string, error) {
	return walk(
		ctx,
		maxPages,
		visit,
		func(ctx context.Context, index int) ([]AssetRecord, Pagination, error) {
			q.PageIndex = index
			p, err := c.Assets(ctx, q)
			return p.Assets, p.Pagination, err
		},
	)
}

// WalkAssignments retains the original incremental filter across all pages.
func (c *Client) WalkAssignments(
	ctx context.Context,
	q AssignmentsQuery,
	maxPages int,
	visit func(Assignment) error,
) (string, error) {
	return walk(
		ctx,
		maxPages,
		visit,
		func(ctx context.Context, index int) ([]Assignment, Pagination, error) {
			q.PageIndex = index
			p, err := c.Assignments(ctx, q)
			return p.Assignments, p.Pagination, err
		},
	)
}

// WalkUsers returns the first page's version after successfully visiting every
// page. An empty page with nextPageIndex is followed normally.
func (c *Client) WalkUsers(
	ctx context.Context,
	q UsersQuery,
	maxPages int,
	visit func(User) error,
) (string, error) {
	return walk(
		ctx,
		maxPages,
		visit,
		func(ctx context.Context, index int) ([]User, Pagination, error) {
			q.PageIndex = index
			p, err := c.Users(ctx, q)
			return p.Users, p.Pagination, err
		},
	)
}

// walk visits successive service pages and stops on callback, request, or pagination
// failure.
func walk[T any](
	ctx context.Context,
	maxPages int,
	visit func(T) error,
	fetch func(context.Context, int) ([]T, Pagination, error),
) (string, error) {
	if maxPages <= 0 || visit == nil {
		return "", ErrInput
	}
	index := 0
	version := ""
	for page := 0; page < maxPages; page++ {
		items, p, err := fetch(ctx, index)
		if err != nil {
			return "", err
		}
		if page == 0 {
			version = p.VersionID
		}
		for _, item := range items {
			if err := visit(item); err != nil {
				return "", fmt.Errorf("appsbooks: visit: %w", err)
			}
		}
		if p.NextPageIndex == nil {
			return version, nil
		}
		index = *p.NextPageIndex
	}
	return "", fmt.Errorf("%w: page cap exceeded", ErrLimit)
}
