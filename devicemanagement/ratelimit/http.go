package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// PeerKey uses the socket peer unless it is explicitly trusted to set
// X-Forwarded-For. It walks from the nearest proxy toward the client, stopping at
// the first untrusted address. Malformed chains fall back to the socket peer.
func PeerKey(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	ip = ip.Unmap()
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	if !isTrusted(ip) {
		return ip.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 32 {
		return ip.String()
	}
	peer := ip
	for i := len(chain) - 1; i >= 0; i-- {
		next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return ip.String()
		}
		peer = next.Unmap()
		if !isTrusted(peer) {
			break
		}
	}
	return peer.String()
}

// HTTPConfig selects quotas before parsing an inbound request body. A nil bucket
// list exempts the route. Reject may encode a protocol-specific error such as ACME.
type HTTPConfig struct {
	Limiter Checker
	Buckets func(*http.Request) []Bucket
	Reject  func(http.ResponseWriter, *http.Request, int)
}

// Middleware enforces configured quotas. Unavailability answers 503 and a denied
// quota answers 429 with a Retry-After rounded up to whole seconds.
func Middleware(cfg HTTPConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Limiter == nil || cfg.Buckets == nil {
			next.ServeHTTP(w, r)
			return
		}
		b := cfg.Buckets(r)
		if len(b) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		d, err := cfg.Limiter.Check(r.Context(), b)
		status := 0
		if err != nil {
			status = http.StatusServiceUnavailable
		} else if !d.Allowed {
			status = http.StatusTooManyRequests
			w.Header().Set("Retry-After", strconv.FormatInt(max(1, int64((d.RetryAfter+time.Second-1)/time.Second)), 10))
		}
		if status != 0 {
			w.Header().Set("Cache-Control", "no-store")
			if cfg.Reject != nil {
				cfg.Reject(w, r, status)
			} else {
				http.Error(w, http.StatusText(status), status)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}
