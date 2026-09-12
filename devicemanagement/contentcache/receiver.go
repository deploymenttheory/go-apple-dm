package contentcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// DefaultMaxBodyBytes bounds a report to one MiB unless configured otherwise.
const DefaultMaxBodyBytes int64 = 1 << 20

// ErrConfig identifies an incomplete receiver configuration.
var ErrConfig = errors.New("contentcache: invalid receiver configuration")

// Config supplies deployment policy. Both callbacks must be safe for concurrent
// requests. TLS, routing and middleware belong to the embedding application.
type Config struct {
	// Authorize checks the request's identity before its body is read. It must
	// validate trusted credentials; hostname and serverGUID are report data,
	// not authenticated identities. Return nil only to authorize ingestion.
	Authorize func(context.Context, *http.Request) error
	// Accept stores or hands off a report. Nil means the caller has accepted
	// responsibility for it. No internal queue or persistence is provided.
	Accept func(context.Context, *Report) error
	// MaxBodyBytes defaults to DefaultMaxBodyBytes; negative values are invalid.
	MaxBodyBytes int64
}

// NewReceiver returns a handler accepting POST and PUT at its mounted route.
// Apple's OpenAPI specifies POST /metrics; its DDM content-cache declaration
// specifies PUT to ManagementStatusTarget. Both carry the same report here.
// Successful handoff returns 202. Invalid reports, authentication failures,
// methods, oversized bodies, media types and sink failures return respectively
// 400, 401, 405, 413, 415 and 503. Callback errors are not exposed to clients.
func NewReceiver(cfg Config) (http.Handler, error) {
	if cfg.Authorize == nil || cfg.Accept == nil || cfg.MaxBodyBytes < 0 {
		return nil, fmt.Errorf("%w: authorization, acceptance and a non-negative body limit are required", ErrConfig)
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			w.Header().Set("Allow", "POST, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := cfg.Authorize(r.Context(), r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		body := http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			code := http.StatusBadRequest
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				code = http.StatusRequestEntityTooLarge
			}
			http.Error(w, http.StatusText(code), code)
			return
		}
		report, err := Decode(data)
		if err != nil {
			http.Error(w, "invalid report", http.StatusBadRequest)
			return
		}
		if r.Context().Err() != nil {
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		}
		if err := cfg.Accept(r.Context(), report); err != nil {
			http.Error(w, "report acceptance unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}), nil
}
