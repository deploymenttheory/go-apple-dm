package proxyclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/internal/proxywire"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// DefaultTimeout bounds one forwarded check-in.
const DefaultTimeout = 30 * time.Second

// Errors.
var (
	// ErrUpstream wraps every failure of the ddm role or the network.
	ErrUpstream = errors.New("proxyclient: upstream failure")
	// ErrBadURL is returned by Handler for an empty or unusable URL.
	ErrBadURL = errors.New("proxyclient: invalid URL")
)

// Config builds a Handler.
type Config struct {
	// AllowInsecureForTests permits HTTP only to a literal loopback address.
	AllowInsecureForTests bool
	// URL is the base URL of the ddm role; proxywire.Path is appended.
	URL string
	// Client defaults to an http.Client with DefaultTimeout. Set TLS
	// client certificates on its transport for mutual TLS.
	Client *http.Client
	// Timeout bounds each request through its context; default
	// DefaultTimeout.
	Timeout time.Duration
	// SendKey signs every request; RecvKey verifies every response.
	SendKey, RecvKey []byte
	// MaxBody bounds the response body; default proxywire.DefaultMaxBody.
	MaxBody int64
	// Auth, when set, adds headers to every request (a bearer token, for
	// example).
	Auth func(*http.Request)
}

// Handler validates cfg and returns the forwarding handler.
func Handler(cfg Config) (service.DMHandler, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%w: empty", ErrBadURL)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf(
			"%w: %q needs an http or https scheme and a host",
			ErrBadURL,
			cfg.URL,
		)
	}
	if !proxywire.ValidKeys(cfg.SendKey, cfg.RecvKey) {
		return nil, fmt.Errorf("%w: independent keys of at least 32 bytes required", ErrUpstream)
	}
	ip := net.ParseIP(u.Hostname())
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(cfg.AllowInsecureForTests && ip != nil && ip.IsLoopback())) {
		return nil, ErrBadURL
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: DefaultTimeout}
	}
	clone := *cfg.Client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.Client = &clone
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	c := &client{cfg: cfg, target: u.JoinPath(proxywire.Path).String()}
	return c.handle, nil
}

type client struct {
	cfg    Config
	target string
}

func (c *client) handle(
	ctx context.Context,
	_ *mdm.Request,
	ck *mdm.Checkin,
	_ *checkin.DeclarativeManagement,
) (service.DMResponse, error) {
	if ck == nil || len(ck.Raw) == 0 {
		return service.DMResponse{}, &service.Error{
			Code: service.CodeBadRequest,
			Err:  fmt.Errorf("%w: check-in without raw bytes", service.ErrInvalidMessage),
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.target, bytes.NewReader(ck.Raw))
	if err != nil {
		return service.DMResponse{}, internal(fmt.Errorf("%w: %w", ErrUpstream, err))
	}
	req.Header.Set("Content-Type", proxywire.ContentType)
	if c.cfg.SendKey != nil {
		signature, err := proxywire.SignRequest(c.cfg.SendKey, req, ck.Raw)
		if err != nil {
			return service.DMResponse{}, internal(err)
		}
		req.Header.Set(proxywire.HeaderSignature, signature)
	}
	if c.cfg.Auth != nil {
		c.cfg.Auth(req)
	}
	resp, err := c.cfg.Client.Do(req)
	if err != nil {
		return service.DMResponse{}, internal(fmt.Errorf("%w: %w", ErrUpstream, err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := proxywire.ReadBody(resp.Body, c.cfg.MaxBody)
	if err != nil {
		return service.DMResponse{}, internal(fmt.Errorf("%w: %w", ErrUpstream, err))
	}
	// Authentication binds the response to this request and its representation.
	if len(resp.Header.Values(proxywire.HeaderSignature)) != 1 {
		return service.DMResponse{}, internal(proxywire.ErrBadSignature)
	}
	if c.cfg.RecvKey != nil {
		if err := proxywire.VerifyBoundResponse(
			c.cfg.RecvKey,
			resp.Header.Get(proxywire.HeaderSignature),
			req.Header.Get(proxywire.HeaderSignature),
			resp.StatusCode,
			resp.Header.Get("Content-Type"),
			body,
		); err != nil {
			return service.DMResponse{}, internal(fmt.Errorf("%w: response: %w", ErrUpstream, err))
		}
	}
	return relay(resp, body)
}

// relay maps the ddm role's answer to the device-facing response: Apple's
// statuses pass through, a 400 is the device's fault, everything else is
// an upstream failure the device must not see.
func relay(resp *http.Response, body []byte) (service.DMResponse, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		return service.DMResponse{Body: body, ContentType: ct, Status: http.StatusOK}, nil
	case http.StatusNotFound:
		return service.DMResponse{Status: http.StatusNotFound}, nil
	case http.StatusBadRequest:
		return service.DMResponse{}, &service.Error{Code: service.CodeBadRequest, Err: fmt.Errorf("%w: rejected the check-in (400)", ErrUpstream)}
	}
	return service.DMResponse{}, internal(fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode))
}

func internal(err error) error {
	return &service.Error{Code: service.CodeInternal, Err: err}
}
