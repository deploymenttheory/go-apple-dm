package appsbooks

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) request(
	ctx context.Context,
	method string,
	u *url.URL,
	in, out any,
	authenticated bool,
) error {
	var body []byte
	if in != nil {
		var err error
		body, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%w: cannot encode request", ErrInput)
		}
		if len(body) > maxBody {
			return fmt.Errorf("%w: request too large", ErrInput)
		}
	}
	for attempt := 0; ; attempt++ {
		err := c.attempt(ctx, method, u, body, out, authenticated)
		var api *APIError
		if err == nil || method != http.MethodGet || attempt >= c.retries ||
			!errors.As(err, &api) ||
			(api.HTTPStatus != 429 && api.HTTPStatus < 500) {
			return err
		}
		if err := c.wait(
			ctx,
			max(api.RetryAfter, time.Second*time.Duration(1<<attempt)),
		); err != nil {
			return err
		}
	}
}

func (c *Client) attempt(
	ctx context.Context,
	method string,
	u *url.URL,
	body []byte,
	out any,
	authenticated bool,
) error {
	if err := ctx.Err(); err != nil {
		return &TransportError{cause: err}
	}
	if authenticated && !c.clock.Now().Before(c.expires) {
		return ErrExpired
	}
	if err := c.wait(ctx, c.nextRequest.Sub(c.clock.Now())); err != nil {
		return err
	}
	if rate := c.service.Limits["maxRequestPerSecond"]; rate > 0 {
		c.nextRequest = c.clock.Now().Add(time.Second / time.Duration(rate))
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return &TransportError{cause: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "go-apple-dm/appsbooks")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return &TransportError{cause: err}
	}
	defer func(body io.Closer) { _ = body.Close() }(res.Body)
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return &TransportError{cause: err}
	}
	if len(data) > maxBody {
		return fmt.Errorf("%w: response too large", ErrProtocol)
	}
	var failure Failure
	decodeErr := json.Unmarshal(data, &failure)
	if res.StatusCode < 200 || res.StatusCode >= 300 || failure.Number != 0 {
		return &APIError{
			Failure:    failure,
			HTTPStatus: res.StatusCode,
			RetryAfter: retryAfter(res.Header.Get("Retry-After"), c.clock.Now()),
		}
	}
	if decodeErr != nil || json.Unmarshal(data, out) != nil {
		return ErrProtocol
	}
	if authenticated {
		var meta ResponseMeta
		if json.Unmarshal(data, &meta) != nil {
			return ErrProtocol
		}
		return c.checkMeta(meta)
	}
	return nil
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, date.Sub(now))
	}
	return 0
}
