package axm

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
)

// DefaultActivityLogMaxBytes bounds the buffered activity CSV (32 MiB).
const DefaultActivityLogMaxBytes int64 = 32 << 20

// FetchActivityLog downloads and validates a complete activity CSV. It accepts
// the configured API origin and Apple's documented HTTPS storage origin,
// store.blobstore.apple.com on port 443. API-origin requests use API credentials;
// presigned storage requests never use bearer tokens or a cookie jar. Redirects
// are rejected. Errors omit the URL and response body because either may contain
// signed credentials. The caller closes the returned reader. Reading it preserves
// every CSV field, including per-device errors and unknown columns.
//
// Downloads are buffered up to Config.ActivityLogMaxBytes and require a CSV
// header and at least one consistent data row. This checks structural integrity,
// not device success or completeness against an intended set of devices; callers
// must compare the rows with their request and inspect each device outcome.
func (c *Client) FetchActivityLog(ctx context.Context, downloadURL string) (io.ReadCloser, error) {
	u, err := httpsurl.Parse(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("%w: activity download URL", ErrArgument)
	}
	apiOrigin := u.Scheme == c.base.Scheme && u.Host == c.base.Host
	storageOrigin := u.Hostname() == "store.blobstore.apple.com" && (u.Port() == "" || u.Port() == "443")
	if !apiOrigin && !storageOrigin {
		return nil, ErrForeignHost
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: activity download request", ErrArgument)
	}
	req.Header.Set("Accept", "text/csv, application/octet-stream")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	if apiOrigin && !storageOrigin {
		tok, tokenErr := c.token(ctx, "")
		if tokenErr != nil {
			return nil, tokenErr
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// Isolate cookies and redirects even when the configured API client has a jar.
	downloadClient := *c.http
	downloadClient.Jar = nil
	resp, err := downloadClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrTransport, ctx.Err())
		}
		return nil, fmt.Errorf("%w: activity download exchange", ErrTransport)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Status: resp.StatusCode, Method: http.MethodGet, URL: "activity download"}
	}
	limit := c.cfg.ActivityLogMaxBytes
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: size limit exceeded", ErrActivityLog)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrTransport, ctx.Err())
		}
		return nil, fmt.Errorf("%w: incomplete activity download", ErrActivityLog)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: size limit exceeded", ErrActivityLog)
	}
	if err := validateActivityCSV(body); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// validateActivityCSV checks header uniqueness and complete, consistent CSV records.
func validateActivityCSV(body []byte) error {
	r := csv.NewReader(bytes.NewReader(body))
	header, err := r.Read()
	if err != nil || len(header) < 2 {
		return fmt.Errorf("%w: missing CSV header", ErrActivityLog)
	}
	columns := make(map[string]bool, len(header))
	for _, name := range header {
		if name == "" || columns[name] {
			return fmt.Errorf("%w: invalid CSV header", ErrActivityLog)
		}
		columns[name] = true
	}
	rows := 0
	for {
		_, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: malformed CSV row", ErrActivityLog)
		}
		rows++
	}
	if rows == 0 {
		return fmt.Errorf("%w: missing CSV outcomes", ErrActivityLog)
	}
	return nil
}

// ActivityLogColumns identifies the actual serial and outcome headers and the
// exact successful outcome value in a downloaded log. Apple's activity endpoint
// documents a CSV URL without fixing its column schema; callers supply the
// observed schema explicitly rather than relying on inferred header aliases.
type ActivityLogColumns struct {
	Serial  string
	Outcome string
	Success string
}

// ActivityLog retains all columns and per-device rows, including vendor failure
// details. Complete means the log contains exactly one outcome for each expected
// serial; Succeeded additionally requires every outcome to equal the configured
// successful value. Neither is set when verification fails.
type ActivityLog struct {
	Header    []string
	Rows      []map[string]string
	Complete  bool
	Succeeded bool
}

// VerifyActivityLog verifies a structurally valid CSV against an explicit schema
// and expected serial set. Missing/unknown columns, duplicate or unexpected
// serials, missing expected devices and empty outcomes return ErrActivityLog.
// Valid per-device failure outcomes remain in Rows and yield Complete=true,
// Succeeded=false, with no decoding error. Partial evidence is returned with an
// error, but never marked complete or successful. The reader is not closed.
// The input is bounded by DefaultActivityLogMaxBytes.
func VerifyActivityLog(reader io.Reader, columns ActivityLogColumns, expectedSerials []string) (ActivityLog, error) {
	var result ActivityLog
	if reader == nil || columns.Serial == "" || columns.Outcome == "" || columns.Success == "" || columns.Serial == columns.Outcome || len(expectedSerials) == 0 {
		return result, fmt.Errorf("%w: activity log schema and serial set required", ErrArgument)
	}
	expected := make(map[string]bool, len(expectedSerials))
	for _, serial := range expectedSerials {
		if serial == "" {
			return result, fmt.Errorf("%w: empty expected serial", ErrArgument)
		}
		if _, exists := expected[serial]; exists {
			return result, fmt.Errorf("%w: duplicate expected serial", ErrArgument)
		}
		expected[serial] = false
	}
	body, err := io.ReadAll(io.LimitReader(reader, DefaultActivityLogMaxBytes+1))
	if err != nil || int64(len(body)) > DefaultActivityLogMaxBytes {
		return result, fmt.Errorf("%w: incomplete or oversized CSV", ErrActivityLog)
	}
	if err := validateActivityCSV(body); err != nil {
		return result, err
	}
	r := csv.NewReader(bytes.NewReader(body))
	result.Header, _ = r.Read() // validateActivityCSV checked every record.
	serialColumn, outcomeColumn := -1, -1
	for i, name := range result.Header {
		if name == columns.Serial {
			serialColumn = i
		}
		if name == columns.Outcome {
			outcomeColumn = i
		}
	}
	if serialColumn < 0 || outcomeColumn < 0 {
		return result, fmt.Errorf("%w: required outcome columns absent", ErrActivityLog)
	}
	successful := true
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, fmt.Errorf("%w: malformed CSV row", ErrActivityLog)
		}
		values := make(map[string]string, len(row))
		for i, value := range row {
			values[result.Header[i]] = value
		}
		result.Rows = append(result.Rows, values)
		serial, outcome := row[serialColumn], row[outcomeColumn]
		seen, exists := expected[serial]
		if !exists || seen || outcome == "" {
			return result, fmt.Errorf("%w: unexpected, repeated or missing device outcome", ErrActivityLog)
		}
		expected[serial] = true
		successful = successful && outcome == columns.Success
	}
	for _, seen := range expected {
		if !seen {
			return result, fmt.Errorf("%w: missing expected device outcome", ErrActivityLog)
		}
	}
	result.Complete = true
	result.Succeeded = successful
	return result, nil
}
