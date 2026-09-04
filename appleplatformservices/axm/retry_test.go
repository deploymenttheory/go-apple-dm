package axm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRetry(t *testing.T) {
	t.Parallel()
	t.Run("RetryAfterSeconds", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.RateLimit(1, "2")
		c := f.client(t, nil)
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		delays := f.clock.recorded()
		if len(delays) != 1 || delays[0] != 2*time.Second {
			t.Fatalf("delays %v, want [2s]", delays)
		}
		if n := len(apiRequests(f.srv)); n != 2 {
			t.Fatalf("API requests %d, want 2", n)
		}
	})
	t.Run("RetryAfterDate", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.RateLimit(1, time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))
		c := f.client(t, nil)
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		delays := f.clock.recorded()
		if len(delays) != 1 || delays[0] < 3*time.Second || delays[0] > 5*time.Second {
			t.Fatalf("delays %v, want about 4s", delays)
		}
		// A missing or unparsable Retry-After falls back to the backoff.
		f.srv.RateLimit(1, "soon")
		f.clock.delays = nil
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if delays := f.clock.recorded(); len(delays) != 1 || delays[0] > 2*time.Millisecond {
			t.Fatalf("fallback delays %v", delays)
		}
		if got := parseRetryAfter("-5", time.Time{}); got != 0 {
			t.Fatalf("negative seconds: %v", got)
		}
		if got := parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), time.Time{}); got != 0 {
			t.Fatalf("past date: %v", got)
		}
	})
	t.Run("BackoffJittered", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.ServerError(3)
		c := f.client(t, func(cfg *Config) {
			cfg.Retry = Retry{Max: 3, Base: 100 * time.Millisecond, Cap: 250 * time.Millisecond}
		})
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		delays := f.clock.recorded()
		if len(delays) != 3 {
			t.Fatalf("delays %v, want 3", delays)
		}
		bounds := [][2]time.Duration{{50 * time.Millisecond, 150 * time.Millisecond}, {100 * time.Millisecond, 300 * time.Millisecond}, {125 * time.Millisecond, 375 * time.Millisecond}}
		for i, d := range delays {
			if d < bounds[i][0] || d >= bounds[i][1] {
				t.Errorf("delay %d = %v, want in [%v, %v)", i, d, bounds[i][0], bounds[i][1])
			}
		}
		distinct := map[time.Duration]struct{}{}
		for range 20 {
			distinct[c.backoff(1)] = struct{}{}
		}
		if len(distinct) < 2 {
			t.Fatal("backoff is not jittered")
		}
	})
	t.Run("BoundedCount", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		f.srv.ServerError(10)
		c := f.client(t, func(cfg *Config) { cfg.Retry.Max = 2 })
		_, err := c.ListOrgDevices(context.Background(), ListOptions{})
		var e *Error
		if !errors.As(err, &e) || e.Status != http.StatusServiceUnavailable {
			t.Fatalf("want 503 after retries: %v", err)
		}
		if n := len(apiRequests(f.srv)); n != 3 {
			t.Fatalf("API requests %d, want 1 + 2 retries", n)
		}
		f.srv.ServerError(0)
		f.srv.RateLimit(10, "1")
		f.srv.Reset()
		_, err = c.ListOrgDevices(context.Background(), ListOptions{})
		if !IsRateLimited(err) {
			t.Fatalf("want 429 after retries: %v", err)
		}
		if n := len(apiRequests(f.srv)); n != 3 {
			t.Fatalf("429 API requests %d, want 3", n)
		}
		var re *Error
		errors.As(err, &re)
		if re.RetryAfter != time.Second {
			t.Fatalf("RetryAfter %v", re.RetryAfter)
		}
	})
	t.Run("NoRetryOnPOSTByDefault", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		id := f.srv.AddMDMServer("m", nil)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		f.srv.ServerError(1)
		if _, err := c.AssignDevices(context.Background(), id, []string{"S1"}); !hasStatus(err, http.StatusServiceUnavailable) {
			t.Fatalf("POST must not retry: %v", err)
		}
		if n := len(apiRequests(f.srv)); n != 1 {
			t.Fatalf("API requests %d, want 1", n)
		}
		f.srv.RateLimit(1, "1")
		f.srv.Reset()
		if _, err := c.AssignDevices(context.Background(), id, []string{"S1"}); !IsRateLimited(err) {
			t.Fatalf("POST must not retry a 429 either: %v", err)
		}
		f.srv.ServerError(1)
		f.srv.Reset()
		req, _ := NewActivityRequest(ActivityAssignDevices, id, []string{"S1"}, time.Time{}, time.Now())
		if _, err := c.CreateOrgDeviceActivity(context.Background(), req, WithIdempotent()); err != nil {
			t.Fatalf("WithIdempotent must retry: %v", err)
		}
		if n := len(apiRequests(f.srv)); n != 2 {
			t.Fatalf("API requests %d, want 2", n)
		}
	})
	t.Run("NoRetryOn4xx", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		if _, err := c.GetOrgDevice(context.Background(), "missing", GetOptions{}); !IsNotFound(err) {
			t.Fatalf("%v", err)
		}
		if n := len(apiRequests(f.srv)); n != 1 {
			t.Fatalf("API requests %d, want 1", n)
		}
		if len(f.clock.recorded()) != 0 {
			t.Fatal("no delay expected")
		}
	})
	t.Run("TransportErrors", func(t *testing.T) {
		t.Parallel()
		srv := stub(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":[]}`))
		})
		flaky := &flakyTransport{next: srv.Client().Transport, fail: 1}
		c := stubClient(t, srv, func(cfg *Config) {
			cfg.Retry.Max = 2
			cfg.HTTPClient = &http.Client{Transport: flaky}
		})
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("retry after a dropped connection: %v", err)
		}
		flaky.fail = 1
		c = stubClient(t, srv, func(cfg *Config) { cfg.HTTPClient = &http.Client{Transport: flaky} })
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); !errors.Is(err, ErrTransport) {
			t.Fatalf("no retries: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.ListOrgDevices(ctx, ListOptions{}); !errors.Is(err, ErrTransport) || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
	})
	t.Run("WaitHonoursContext", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.ServerError(1)
		c := f.client(t, func(cfg *Config) { cfg.Clock = nil })
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		c.cfg.Retry = Retry{Max: 1, Base: time.Hour, Cap: time.Hour}
		if _, err := c.ListOrgDevices(ctx, ListOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait must stop on cancel: %v", err)
		}
	})
}

func TestErrors(t *testing.T) {
	t.Parallel()
	t.Run("Decode", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		_, err := c.GetOrgDevice(context.Background(), "XABC", GetOptions{})
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("%T: %v", err, err)
		}
		if e.Status != http.StatusNotFound || e.Method != http.MethodGet || !strings.HasSuffix(e.URL, "/v1/orgDevices/XABC") {
			t.Fatalf("%+v", e)
		}
		if len(e.Errors) != 1 || e.Errors[0].Code != "RESOURCE_NOT_FOUND" || e.Errors[0].Status != "404" || e.Errors[0].ID == "" || e.Errors[0].Title == "" || !strings.Contains(e.Errors[0].Detail, "XABC") {
			t.Fatalf("%+v", e.Errors)
		}
		if msg := e.Error(); !strings.Contains(msg, "404") || !strings.Contains(msg, "RESOURCE_NOT_FOUND") || !strings.Contains(msg, "XABC") {
			t.Fatalf("message %q", msg)
		}
		if e.Code() != "RESOURCE_NOT_FOUND" {
			t.Fatal(e.Code())
		}
	})
	t.Run("BothSourceForms", func(t *testing.T) {
		t.Parallel()
		var doc ErrorResponse
		body := `{"errors":[
			{"status":"400","code":"A","title":"t","detail":"d","source":{"pointer":"/data/attributes/name"}},
			{"status":"400","code":"B","title":"t","detail":"d","source":{"parameter":"fields[orgDevices]"}},
			{"status":"400","code":"C","title":"t","detail":"d","source":{"jsonPointer":{"pointer":"/data/id"}}},
			{"status":"400","code":"D","title":"t","detail":"d","source":{"parameter":{"parameter":"limit"}}},
			{"status":"400","code":"E","title":"t","detail":"d","source":{}}
		]}`
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatal(err)
		}
		want := []ErrorSource{{Pointer: "/data/attributes/name"}, {Parameter: "fields[orgDevices]"}, {Pointer: "/data/id"}, {Parameter: "limit"}, {}}
		for i, w := range want {
			if got := *doc.Errors[i].Source; got != w {
				t.Errorf("error %d source %+v, want %+v", i, got, w)
			}
		}
		e := &Error{Status: 400, Method: "GET", URL: "u", Errors: doc.Errors[:2]}
		if msg := e.Error(); !strings.Contains(msg, "parameter fields[orgDevices]") || !strings.Contains(msg, "at /data/attributes/name") {
			t.Fatalf("message %q", msg)
		}
		var src ErrorSource
		if err := json.Unmarshal([]byte(`[1]`), &src); !errors.Is(err, ErrDecode) {
			t.Fatalf("bad source: %v", err)
		}
		if err := json.Unmarshal([]byte(`{"pointer":7,"parameter":[1]}`), &src); err != nil || src != (ErrorSource{}) {
			t.Fatalf("odd source types: %+v %v", src, err)
		}
	})
	t.Run("Multiple", func(t *testing.T) {
		t.Parallel()
		srv := stub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Request-Id", "req-1")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"errors":[{"status":"409","code":"ONE","title":"t","detail":"first"},{"status":"422","code":"TWO","title":"t","detail":"second","links":{"about":"https://x"},"meta":{"k":1}}]}`))
		})
		c := stubClient(t, srv, nil)
		_, err := c.ListOrgDevices(context.Background(), ListOptions{})
		var e *Error
		if !errors.As(err, &e) || !IsConflict(err) {
			t.Fatalf("%v", err)
		}
		if len(e.Errors) != 2 || e.Errors[1].Code != "TWO" || e.Errors[1].Links.About != "https://x" || string(e.Errors[1].Meta) != `{"k":1}` || e.RequestID != "req-1" {
			t.Fatalf("%+v", e)
		}
		if msg := e.Error(); !strings.Contains(msg, "ONE") || !strings.Contains(msg, "TWO (second)") {
			t.Fatalf("message %q", msg)
		}
	})
	t.Run("NonJSON", func(t *testing.T) {
		t.Parallel()
		srv := stub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>" + strings.Repeat("x", 300)))
		})
		c := stubClient(t, srv, nil)
		_, err := c.ListOrgDevices(context.Background(), ListOptions{})
		var e *Error
		if !errors.As(err, &e) || e.Status != http.StatusBadGateway || len(e.Errors) != 0 || !strings.HasPrefix(string(e.Body), "<html>") {
			t.Fatalf("%v", err)
		}
		if msg := e.Error(); !strings.Contains(msg, "502") || !strings.HasSuffix(msg, "...") || len(msg) > 300 {
			t.Fatalf("message %q", msg)
		}
		if e.Code() != "" {
			t.Fatal(e.Code())
		}
	})
	t.Run("Helpers", func(t *testing.T) {
		t.Parallel()
		if IsNotFound(nil) || IsConflict(nil) || IsUnauthorized(nil) || IsRateLimited(nil) {
			t.Fatal("nil error")
		}
		plain := errors.New("plain")
		if IsNotFound(plain) || IsConflict(plain) || IsUnauthorized(plain) || IsRateLimited(plain) {
			t.Fatal("plain error")
		}
		for status, check := range map[int]func(error) bool{404: IsNotFound, 409: IsConflict, 401: IsUnauthorized, 429: IsRateLimited} {
			err := fmtWrap(&Error{Status: status})
			if !check(err) {
				t.Errorf("%d not recognised", status)
			}
			if IsNotFound(err) && status != 404 || IsConflict(err) && status != 409 {
				t.Errorf("%d misclassified", status)
			}
		}
		ae := &AuthError{Status: 400, Code: "invalid_client", Description: "bad", Err: plain}
		if !IsUnauthorized(ae) || !errors.Is(ae, plain) {
			t.Fatal("AuthError")
		}
		if msg := ae.Error(); !strings.Contains(msg, "400") || !strings.Contains(msg, "invalid_client (bad)") || !strings.Contains(msg, "plain") {
			t.Fatalf("message %q", msg)
		}
		if (&AuthError{}).Error() != "axm: authentication failed" {
			t.Fatal((&AuthError{}).Error())
		}
	})
}

func fmtWrap(err error) error { return errors.Join(err) }

// flakyTransport fails the first fail API round trips with a connection
// error; the token endpoint always goes through.
type flakyTransport struct {
	next http.RoundTripper
	fail int
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(r.URL.Path, "/token") && f.fail > 0 {
		f.fail--
		return nil, errors.New("connection reset by peer")
	}
	return f.next.RoundTrip(r)
}
