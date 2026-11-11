package axm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// throttleClient uses independent in-memory HTTP exchanges and real timer semantics
// inside synctest; it never contacts Apple or depends on an external service.
func throttleClient(t *testing.T, handler activityTransport, policy Retry) *Client {
	t.Helper()
	c, err := New(t.Context(), Config{ClientID: testClientID, KeyID: testKeyID, PrivateKey: newKey(t), HTTPClient: &http.Client{Transport: handler}, Retry: policy})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// throttleResponse owns an independent response body and optional provider delay.
func throttleResponse(r *http.Request, status int, after, body string) *http.Response {
	h := make(http.Header)
	if after != "" {
		h.Set("Retry-After", after)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

const throttleToken = `{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`

// TestTokenThrottleRecovery checks provider delays and fresh assertions before one protected mutation.
func TestTokenThrottleRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, header string
		min, max     time.Duration
	}{
		{"seconds", "2", 2 * time.Second, 2 * time.Second},
		{"date", "date", 2 * time.Second, 2 * time.Second},
		{"absent", "", 500 * time.Millisecond, 1500 * time.Millisecond},
		{"malformed", "later", 500 * time.Millisecond, 1500 * time.Millisecond},
		{"past", "past", 500 * time.Millisecond, 1500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				calls, mutations := 0, 0
				assertions := map[string]bool{}
				header := tc.header
				if header == "date" {
					header = start.Add(2 * time.Second).UTC().Format(http.TimeFormat)
				}
				if header == "past" {
					header = start.Add(-time.Hour).UTC().Format(http.TimeFormat)
				}
				c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "oauth2/token") {
						calls++
						if err := r.ParseForm(); err != nil {
							t.Error(err)
						}
						jwt := r.Form.Get("client_assertion")
						parts := strings.Split(jwt, ".")
						if len(parts) != 3 {
							t.Fatal("missing assertion")
						}
						raw, err := base64.RawURLEncoding.DecodeString(parts[1])
						if err != nil {
							t.Fatal(err)
						}
						var claims AssertionClaims
						if err = json.Unmarshal(raw, &claims); err != nil {
							t.Fatal(err)
						}
						if assertions[claims.Jti] {
							t.Error("assertion reused")
						}
						assertions[claims.Jti] = true
						if calls == 1 {
							return throttleResponse(r, 429, header, `{"error":"invalid_request","error_description":"private-diagnostic"}`), nil
						}
						return throttleResponse(r, 200, "", throttleToken), nil
					}
					mutations++
					return throttleResponse(r, 201, "", `{"data":{"id":"owned","type":"webhooks"}}`), nil
				}, Retry{Max: 2, Base: time.Second, Cap: 4 * time.Second, MaxWait: 10 * time.Second})
				_, err := c.CreateWebhook(t.Context(), WebhookCreateAttributes{Name: "test", URL: "https://receiver.example/callback", Secret: "private", EventTypes: []string{"ORG_DEVICE_ATTRIBUTE_CHANGED"}})
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 || mutations != 1 {
					t.Fatalf("token=%d mutations=%d", calls, mutations)
				}
				if elapsed := time.Since(start); elapsed < tc.min || elapsed > tc.max {
					t.Fatalf("delay=%v", elapsed)
				}
			})
		})
	}
}

// TestTokenThrottlePersistent retains typed failure and cooldown after bounded exhaustion.
func TestTokenThrottlePersistent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			return throttleResponse(r, 429, "60", `{"error":"invalid_request","error_description":"private-diagnostic"}`), nil
		}, Retry{Max: 1, Base: time.Second, Cap: time.Second, MaxWait: 2 * time.Minute})
		_, err := c.Token(t.Context())
		limited, _ := errors.AsType[*RateLimitError](err)
		if limited == nil || limited.Endpoint != "token" || limited.RetryAfter != time.Minute || limited.RetryAt != time.Now().Add(time.Minute) || calls.Load() != 2 {
			t.Fatalf("limit=%v calls=%d", limited, calls.Load())
		}
		if !IsRateLimited(err) || IsUnauthorized(err) || strings.Contains(err.Error(), "private-diagnostic") {
			t.Fatalf("classification/redaction: %v", err)
		}
		original, _ := errors.AsType[*AuthError](err)
		if original == nil || original.Status != 429 || original.Code != "invalid_request" || original.RetryAfter != time.Minute {
			t.Fatal("lost original token error")
		}
		c.cfg.Retry.MaxWait = time.Second
		_, err = c.Token(t.Context())
		if !IsRateLimited(err) || calls.Load() != 2 {
			t.Fatalf("subsequent call ignored cooldown: %v calls=%d", err, calls.Load())
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = c.Token(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

// TestThrottleBudgetAndHeaders rejects early retries and overflowing delay values.
func TestThrottleBudgetAndHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, header string }{{"long", "3600"}, {"overflow", "18446744073709551615"}, {"beyond uint64", "99999999999999999999999999999"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
					calls++
					return throttleResponse(r, 429, tc.header, `{"error":"invalid_request"}`), nil
				}, Retry{Max: 3, Base: time.Second, Cap: time.Second, MaxWait: time.Second})
				start := time.Now()
				_, err := c.Token(t.Context())
				if !IsRateLimited(err) || calls != 1 || time.Since(start) != 0 {
					t.Fatalf("unsafe retry/wait: %v calls=%d", err, calls)
				}
				l, _ := errors.AsType[*RateLimitError](err)
				if l.RetryAfter < time.Hour {
					t.Fatalf("shortened server delay: %v", l)
				}
			})
		})
	}
	t.Run("cumulative", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				return throttleResponse(r, 429, "2", `{}`), nil
			}, Retry{Max: 9, Base: time.Second, Cap: time.Second, MaxWait: 3 * time.Second})
			start := time.Now()
			_, err := c.Token(t.Context())
			if !IsRateLimited(err) || calls != 2 || time.Since(start) != 2*time.Second {
				t.Fatalf("budget: %v calls=%d waited=%v", err, calls, time.Since(start))
			}
		})
	})
	t.Run("deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				return throttleResponse(r, 429, "60", `{}`), nil
			}, Retry{Max: 3, Base: time.Second, Cap: time.Second, MaxWait: time.Hour})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			start := time.Now()
			_, err := c.Token(ctx)
			if !IsRateLimited(err) || ctx.Err() != nil || calls != 1 || time.Since(start) != 0 {
				t.Fatalf("deadline: %v", err)
			}
		})
	})
	t.Run("negative config", func(t *testing.T) {
		_, err := New(t.Context(), Config{ClientID: testClientID, KeyID: testKeyID, PrivateKey: newKey(t), Retry: Retry{MaxWait: -1}})
		if !errors.Is(err, ErrConfig) {
			t.Fatal(err)
		}
	})
}

// TestTokenThrottleConcurrentCancellation lets live callers recover after a cancelled leader.
func TestTokenThrottleConcurrentCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return throttleResponse(r, 429, "2", `{}`), nil
			}
			return throttleResponse(r, 200, "", throttleToken), nil
		}, Retry{Max: 2, Base: time.Second, Cap: time.Second, MaxWait: 10 * time.Second})
		ctx, cancel := context.WithCancel(t.Context())
		leader := make(chan error, 1)
		go func() { _, err := c.Token(ctx); leader <- err }()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("first exchange absent")
		}
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for range 16 {
			wg.Go(func() { _, err := c.Token(t.Context()); errs <- err })
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("parallel token storm")
		}
		cancel()
		synctest.Wait()
		if err := <-leader; !errors.Is(err, context.Canceled) || !IsRateLimited(err) {
			t.Fatalf("lost cancellation/limit: %v", err)
		}
		time.Sleep(2 * time.Second)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("exchanges=%d", calls.Load())
		}
	})
}

// TestAPIThrottleCoordinatesWithoutMutationReplay applies a POST throttle to later reads without replay.
func TestAPIThrottleCoordinatesWithoutMutationReplay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "oauth2/token") {
				return throttleResponse(r, 200, "", throttleToken), nil
			}
			calls++
			if calls == 1 {
				return throttleResponse(r, 429, "2", `{"errors":[{"code":"RATE_LIMIT_EXCEEDED","detail":"private-diagnostic"}]}`), nil
			}
			return throttleResponse(r, 200, "", `{"data":[]}`), nil
		}, Retry{Max: 3, Base: time.Second, Cap: time.Second, MaxWait: 10 * time.Second})
		_, err := c.CreateWebhook(t.Context(), WebhookCreateAttributes{Name: "test", URL: "https://receiver.example", Secret: "private", EventTypes: []string{"ORG_DEVICE_ATTRIBUTE_CHANGED"}})
		limited, _ := errors.AsType[*RateLimitError](err)
		if !IsRateLimited(err) || calls != 1 || limited == nil || limited.Endpoint != "api" || strings.Contains(err.Error(), "private") {
			t.Fatalf("POST replay/classification: %v calls=%d", err, calls)
		}
		start := time.Now()
		if _, err = c.ListWebhooks(t.Context(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || time.Since(start) != 2*time.Second {
			t.Fatalf("cooldown ignored: calls=%d delay=%v", calls, time.Since(start))
		}
	})
}

// TestThrottleClassification separates OAuth rate limits from rejected credentials and server failures.
func TestThrottleClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status     int
		rate, auth bool
	}{{429, true, false}, {400, false, true}, {401, false, true}, {403, false, true}, {503, false, false}} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			t.Parallel()
			err := fmtAuthError(tc.status)
			if IsRateLimited(err) != tc.rate || IsUnauthorized(err) != tc.auth {
				t.Fatalf("status=%d rate=%v auth=%v", tc.status, IsRateLimited(err), IsUnauthorized(err))
			}
		})
	}
}

// fmtAuthError wraps an OAuth status to exercise error-chain classification.
func fmtAuthError(status int) error {
	return errors.Join(errors.New("wrapped"), &AuthError{Status: status})
}

// TestAPIThrottleConcurrentExtension prevents waiting callers from ignoring a later longer cooldown.
func TestAPIThrottleConcurrentExtension(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "oauth2/token") {
				return throttleResponse(r, 200, "", throttleToken), nil
			}
			n := calls.Add(1)
			if n == 1 {
				<-release
				return throttleResponse(r, 429, "6", `{}`), nil
			}
			if n == 2 {
				return throttleResponse(r, 429, "2", `{}`), nil
			}
			return throttleResponse(r, 200, "", `{"data":[]}`), nil
		}, Retry{Max: 0, Base: time.Second, Cap: time.Second, MaxWait: 10 * time.Second})
		create := func() error {
			_, err := c.CreateWebhook(t.Context(), WebhookCreateAttributes{Name: "test", URL: "https://receiver.example", Secret: "private", EventTypes: []string{"ORG_DEVICE_ATTRIBUTE_CHANGED"}})
			return err
		}
		first := make(chan error, 1)
		go func() { first <- create() }()
		synctest.Wait()
		if err := create(); !IsRateLimited(err) {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for range 16 {
			wg.Go(func() { _, err := c.ListWebhooks(t.Context(), ListOptions{}); errs <- err })
		}
		synctest.Wait()
		time.Sleep(time.Second)
		close(release)
		if err := <-first; !IsRateLimited(err) {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(time.Second)
		if calls.Load() != 2 {
			t.Fatal("callers ignored concurrent cooldown extension", calls.Load())
		}
		time.Sleep(5 * time.Second)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if calls.Load() != 18 {
			t.Fatal("unexpected requests", calls.Load())
		}
	})
}

// TestSuccessfulInflightReadPreservesNewThrottle fences an old success against a newer rate limit.
func TestSuccessfulInflightReadPreservesNewThrottle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "oauth2/token") {
				return throttleResponse(r, 200, "", throttleToken), nil
			}
			if calls.Add(1) == 1 {
				<-release
				return throttleResponse(r, 200, "", `{"data":[]}`), nil
			}
			return throttleResponse(r, 429, "60", `{}`), nil
		}, Retry{Max: 0, Base: time.Second, Cap: time.Second, MaxWait: time.Second})
		first := make(chan error, 1)
		go func() { _, err := c.ListWebhooks(t.Context(), ListOptions{}); first <- err }()
		synctest.Wait()
		if _, err := c.ListWebhooks(t.Context(), ListOptions{}); !IsRateLimited(err) {
			t.Fatal(err)
		}
		close(release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListWebhooks(t.Context(), ListOptions{}); !IsRateLimited(err) || calls.Load() != 2 {
			t.Fatal("old success cleared newer cooldown", err, calls.Load())
		}
	})
}

// TestSharedTokenWaitBudgetAndCancellation isolates departing waiters from the active refresh.
func TestSharedTokenWaitBudgetAndCancellation(t *testing.T) {
	t.Parallel()
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				c := throttleClient(t, func(r *http.Request) (*http.Response, error) {
					if calls.Add(1) == 1 {
						return throttleResponse(r, 429, "2", `{}`), nil
					}
					return throttleResponse(r, 200, "", throttleToken), nil
				}, Retry{Max: 2, Base: time.Second, Cap: time.Second, MaxWait: time.Second})
				leader := make(chan error, 1)
				go func() {
					_, err := c.tokenWithBudget(t.Context(), "", &throttleBudget{remaining: 5 * time.Second})
					leader <- err
				}()
				synctest.Wait()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				waiter := make(chan error, 1)
				go func() { _, err := c.Token(ctx); waiter <- err }()
				synctest.Wait()
				if cancelled {
					cancel()
				} else {
					time.Sleep(time.Second)
				}
				err := <-waiter
				if !IsRateLimited(err) || (cancelled && !errors.Is(err, context.Canceled)) || (!cancelled && !errors.Is(err, errRetryBudget)) {
					t.Fatalf("shared wait: %v", err)
				}
				time.Sleep(2 * time.Second)
				if err := <-leader; err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 2 {
					t.Fatal("waiter poisoned shared refresh", calls.Load())
				}
			})
		})
	}
}
