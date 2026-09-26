package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// PathEnrollmentLinks is the public prefix for enrollment-link landing pages and profile
// downloads: PathEnrollmentLinks + token, and PathEnrollmentLinks + token + "/profile".
const PathEnrollmentLinks = "/enroll/links/"

const (
	// DefaultEnrollmentLinkTTL applies when a link request omits TTL.
	DefaultEnrollmentLinkTTL = time.Hour
	// MaxEnrollmentLinkTTL bounds how long an unredeemed link remains usable.
	MaxEnrollmentLinkTTL = 24 * time.Hour
	// enrollmentLinkRetention keeps link metadata listable after expiry or redemption.
	enrollmentLinkRetention = 24 * time.Hour
	enrollmentLinkPrefix    = "enrollment-link:"
)

// Enrollment-link states reported by the admin API.
const (
	EnrollmentLinkActive   = "active"
	EnrollmentLinkRedeemed = "redeemed"
	EnrollmentLinkRevoked  = "revoked"
	EnrollmentLinkExpired  = "expired"
)

var errEnrollmentLinkUnavailable = errors.New("enrollment link unavailable")

// EnrollmentLinkRequest creates a single-use link that issues one enrollment profile. The
// profile fields have the meanings of EnrollmentProfileRequest. TTL is a Go duration
// string; empty selects DefaultEnrollmentLinkTTL and values above MaxEnrollmentLinkTTL are
// rejected.
type EnrollmentLinkRequest struct {
	DeviceID     string              `json:"DeviceID"`
	Serial       string              `json:"Serial"`
	Product      string              `json:"Product"`
	OSVersion    string              `json:"OSVersion"`
	MacHardware  enroll.MacHardware  `json:"MacHardware"`
	Identity     string              `json:"Identity"`
	AccessRights enroll.AccessRights `json:"AccessRights"`
	Scope        string              `json:"Scope"`
	TTL          string              `json:"TTL"`
}

// EnrollmentLink is an enrollment link's retained metadata. ID is the hex SHA-256 digest
// of the link token; the token itself is returned once, at creation, and never stored.
type EnrollmentLink struct {
	ID           string              `json:"ID"`
	State        string              `json:"State"`
	DeviceID     string              `json:"DeviceID"`
	Serial       string              `json:"Serial,omitzero"`
	Product      string              `json:"Product,omitzero"`
	OSVersion    string              `json:"OSVersion,omitzero"`
	MacHardware  enroll.MacHardware  `json:"MacHardware,omitzero"`
	Identity     string              `json:"Identity,omitzero"`
	AccessRights enroll.AccessRights `json:"AccessRights,omitzero"`
	Scope        string              `json:"Scope,omitzero"`
	CreatedBy    string              `json:"CreatedBy,omitzero"`
	CreatedAt    time.Time           `json:"CreatedAt"`
	ExpiresAt    time.Time           `json:"ExpiresAt"`
	RedeemedAt   time.Time           `json:"RedeemedAt,omitzero"`
	RevokedAt    time.Time           `json:"RevokedAt,omitzero"`
}

// EnrollmentLinkCreated is the creation response. URL contains the bearer token.
type EnrollmentLinkCreated struct {
	ID        string    `json:"ID"`
	URL       string    `json:"URL"`
	ExpiresAt time.Time `json:"ExpiresAt"`
}

// state derives the link's current state at now.
func (l EnrollmentLink) state(now time.Time) string {
	switch {
	case !l.RevokedAt.IsZero():
		return EnrollmentLinkRevoked
	case !l.RedeemedAt.IsZero():
		return EnrollmentLinkRedeemed
	case !now.Before(l.ExpiresAt):
		return EnrollmentLinkExpired
	default:
		return EnrollmentLinkActive
	}
}

// profileRequest returns the profile issuance request bound to the link.
func (l EnrollmentLink) profileRequest() EnrollmentProfileRequest {
	return EnrollmentProfileRequest{
		DeviceID: l.DeviceID, Serial: l.Serial, Product: l.Product, OSVersion: l.OSVersion,
		MacHardware: l.MacHardware, Identity: l.Identity, AccessRights: l.AccessRights,
		Scope: l.Scope,
	}
}

// enrollmentLinkID derives the stored identifier from a link token.
func enrollmentLinkID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// enrollmentLinkRoutes declares the admin routes for creating, listing and revoking links.
func (a *App) enrollmentLinkRoutes() []adminRoute {
	return []adminRoute{
		{
			Pattern: "POST /enrollment-links",
			Action:  ActionIssueEnrollmentProfile,
			Family:  "enrollment",
			Handler: http.HandlerFunc(a.createEnrollmentLink),
		},
		{
			Pattern: "GET /enrollment-links",
			Action:  ActionIssueEnrollmentProfile,
			Family:  "enrollment",
			Handler: http.HandlerFunc(a.listEnrollmentLinks),
		},
		{
			Pattern: "DELETE /enrollment-links/{id}",
			Action:  ActionIssueEnrollmentProfile,
			Family:  "enrollment",
			Handler: http.HandlerFunc(a.revokeEnrollmentLink),
		},
	}
}

// createEnrollmentLink validates the request, stores the link metadata under the token
// digest and returns the only copy of the token-bearing URL.
func (a *App) createEnrollmentLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req EnrollmentLinkRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	link, ttl, err := newEnrollmentLink(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	link.CreatedBy = a.actor(r).Name
	token, err := enrollmentLinkToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	link.ID = enrollmentLinkID(token)
	key := enrollmentLinkPrefix + link.ID
	err = a.enroll.state.Update(r.Context(), []string{key}, func(tx state.Tx) error {
		now := tx.Now()
		link.CreatedAt = now
		link.ExpiresAt = now.Add(ttl)
		value, err := json.Marshal(link)
		if err != nil {
			return fmt.Errorf("encode enrollment link: %w", err)
		}
		return tx.Put(r.Context(), state.Record{
			Key: key, Value: value, ExpiresAt: link.ExpiresAt.Add(enrollmentLinkRetention),
		})
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, EnrollmentLinkCreated{
		ID:        link.ID,
		URL:       a.enroll.base + PathEnrollmentLinks + token,
		ExpiresAt: link.ExpiresAt,
	})
}

// newEnrollmentLink validates a request and returns the link's binding and lifetime.
func newEnrollmentLink(req EnrollmentLinkRequest) (EnrollmentLink, time.Duration, error) {
	if req.DeviceID == "" {
		return EnrollmentLink{}, 0, fmt.Errorf("%w: DeviceID is required", errOperation)
	}
	switch req.Identity {
	case "", IdentitySCEP, IdentityACME:
	default:
		return EnrollmentLink{}, 0, fmt.Errorf("%w: Identity must be scep or acme", errOperation)
	}
	if req.Scope != "" && req.Scope != profile.ScopeSystem && req.Scope != profile.ScopeUser {
		return EnrollmentLink{}, 0, fmt.Errorf("%w: Scope must be System or User", errOperation)
	}
	ttl := DefaultEnrollmentLinkTTL
	if req.TTL != "" {
		var err error
		if ttl, err = time.ParseDuration(req.TTL); err != nil || ttl <= 0 || ttl > MaxEnrollmentLinkTTL {
			return EnrollmentLink{}, 0, fmt.Errorf(
				"%w: TTL must be a positive duration of at most %s", errOperation, MaxEnrollmentLinkTTL,
			)
		}
	}
	return EnrollmentLink{
		DeviceID: req.DeviceID, Serial: req.Serial, Product: req.Product, OSVersion: req.OSVersion,
		MacHardware: req.MacHardware, Identity: req.Identity, AccessRights: req.AccessRights,
		Scope: req.Scope,
	}, ttl, nil
}

// enrollmentLinkToken returns 256 random bits encoded for use in a URL path segment.
func enrollmentLinkToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("enrollment link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// listEnrollmentLinks returns one page of retained link metadata in identifier order.
func (a *App) listEnrollmentLinks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	limit := paging.DefaultPageSize
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > paging.MaxPageSize {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%w: limit %q", errOperation, v))
			return
		}
		limit = n
	}
	after := ""
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		after = enrollmentLinkPrefix + cursor
	}
	records, err := a.enroll.state.List(r.Context(), enrollmentLinkPrefix, after, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := a.cfg.Clock.Now()
	result := paging.Result[EnrollmentLink]{Items: []EnrollmentLink{}}
	for i, record := range records {
		if i == limit {
			result.NextCursor = result.Items[len(result.Items)-1].ID
			break
		}
		var link EnrollmentLink
		if err := json.Unmarshal(record.Value, &link); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		link.State = link.state(now)
		result.Items = append(result.Items, link)
	}
	writeJSON(w, http.StatusOK, result)
}

// revokeEnrollmentLink prevents an unredeemed link from issuing a profile. Revoking a
// redeemed link is a conflict; repeating a revocation succeeds without change.
func (a *App) revokeEnrollmentLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	key := enrollmentLinkPrefix + id
	if !state.ValidKey(key) {
		writeError(w, http.StatusNotFound, errEnrollmentLinkUnavailable)
		return
	}
	var link EnrollmentLink
	err := a.enroll.state.Update(r.Context(), []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(r.Context(), key)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(record.Value, &link); err != nil {
			return fmt.Errorf("decode enrollment link: %w", err)
		}
		if !link.RedeemedAt.IsZero() {
			return errEnrollmentLinkRedeemed
		}
		if link.RevokedAt.IsZero() {
			link.RevokedAt = tx.Now()
		}
		value, err := json.Marshal(link)
		if err != nil {
			return fmt.Errorf("encode enrollment link: %w", err)
		}
		record.Value = value
		return tx.Put(r.Context(), record)
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, errEnrollmentLinkUnavailable)
	case errors.Is(err, errEnrollmentLinkRedeemed):
		writeError(w, http.StatusConflict, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		link.State = link.state(a.cfg.Clock.Now())
		writeJSON(w, http.StatusOK, link)
	}
}

var errEnrollmentLinkRedeemed = fmt.Errorf("%w: the enrollment link was already redeemed", errOperation)

// wireEnrollmentLinks mounts the public landing page and single-use profile download.
func (a *App) wireEnrollmentLinks(e *enrollment, mux *http.ServeMux) {
	trust := len(e.trust) > 0
	mux.HandleFunc("GET "+PathEnrollmentLinks+"{token}", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		link, err := a.activeEnrollmentLink(r.Context(), token)
		if err != nil {
			a.enrollmentLinkUnavailable(w, r, err)
			return
		}
		enrollmentLinkHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = enrollmentLinkPage.Execute(w, enrollmentLinkView{
			DeviceID:  link.DeviceID,
			ExpiresAt: link.ExpiresAt.UTC().Format(time.RFC1123),
			Profile:   PathEnrollmentLinks + url.PathEscape(token) + "/profile",
			Trust:     trust,
			TrustPath: PathTrustProfile,
		})
	})
	mux.HandleFunc("GET "+PathEnrollmentLinks+"{token}/profile", a.redeemEnrollmentLink)
}

// activeEnrollmentLink returns the stored link for token when it can still be redeemed.
func (a *App) activeEnrollmentLink(ctx context.Context, token string) (EnrollmentLink, error) {
	if token == "" {
		return EnrollmentLink{}, errEnrollmentLinkUnavailable
	}
	record, err := a.enroll.state.Get(ctx, enrollmentLinkPrefix+enrollmentLinkID(token))
	if err != nil {
		return EnrollmentLink{}, err
	}
	var link EnrollmentLink
	if err := json.Unmarshal(record.Value, &link); err != nil {
		return EnrollmentLink{}, fmt.Errorf("decode enrollment link: %w", err)
	}
	if link.state(a.cfg.Clock.Now()) != EnrollmentLinkActive {
		return EnrollmentLink{}, errEnrollmentLinkUnavailable
	}
	return link, nil
}

// redeemEnrollmentLink atomically consumes an active link, then issues its profile. The
// link is consumed before issuance so concurrent requests cannot both receive a profile;
// an issuance failure leaves the link redeemed and the operator creates another.
func (a *App) redeemEnrollmentLink(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	key := enrollmentLinkPrefix + enrollmentLinkID(token)
	var link EnrollmentLink
	err := a.enroll.state.Update(r.Context(), []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(r.Context(), key)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(record.Value, &link); err != nil {
			return fmt.Errorf("decode enrollment link: %w", err)
		}
		if link.state(tx.Now()) != EnrollmentLinkActive {
			return errEnrollmentLinkUnavailable
		}
		link.RedeemedAt = tx.Now()
		value, err := json.Marshal(link)
		if err != nil {
			return fmt.Errorf("encode enrollment link: %w", err)
		}
		record.Value = value
		return tx.Put(r.Context(), record)
	})
	if err != nil {
		a.enrollmentLinkUnavailable(w, r, err)
		return
	}
	if p := a.cfg.publisher(); p != nil {
		if err := p.Publish(r.Context(), event.Event{
			Type:  event.EnrollmentLinkRedeemed,
			At:    link.RedeemedAt,
			Actor: "enrollment-link",
			Data: map[string]any{
				"link": link.ID, "device": link.DeviceID, "identity": link.Identity,
			},
		}); err != nil {
			a.cfg.Logger.ErrorContext(r.Context(), "enrollment link redemption not recorded", "error", err)
		}
	}
	b, err := a.ExportEnrollmentProfile(r.Context(), link.profileRequest())
	if err != nil {
		a.cfg.Logger.WarnContext(r.Context(), "enrollment link issuance failed", "link", link.ID, "error", err)
		enrollmentLinkHeaders(w)
		http.Error(w, "enrollment profile unavailable", http.StatusInternalServerError)
		return
	}
	enrollmentLinkHeaders(w)
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="enrollment.mobileconfig"`)
	_, _ = w.Write(b)
}

// enrollmentLinkUnavailable writes the same response for unknown, expired, redeemed and
// revoked links, and logs storage failures without exposing them.
func (a *App) enrollmentLinkUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, errEnrollmentLinkUnavailable) && !errors.Is(err, state.ErrNotFound) {
		a.cfg.Logger.ErrorContext(r.Context(), "enrollment link lookup failed", "error", err)
	}
	enrollmentLinkHeaders(w)
	http.Error(w, "enrollment link unavailable", http.StatusNotFound)
}

// enrollmentLinkHeaders prevents caching, framing and Referer disclosure of token URLs.
func enrollmentLinkHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
}

type enrollmentLinkView struct {
	DeviceID, ExpiresAt, Profile, TrustPath string
	Trust                                   bool
}

var enrollmentLinkPage = template.Must(template.New("enrollment-link").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Device enrollment</title>
<style>
body{font:16px/1.5 -apple-system,system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem;color:#1d1d1f;background:#fff}
a.button{display:inline-block;padding:.6rem 1rem;border-radius:.5rem;background:#0071e3;color:#fff;text-decoration:none}
ol li{margin-bottom:1rem}
code{font-size:.9em}
@media (prefers-color-scheme:dark){body{color:#f5f5f7;background:#1d1d1f}}
</style>
</head>
<body>
<h1>Device enrollment</h1>
<p>This link enrolls device <code>{{.DeviceID}}</code> in device management. It can be used once and expires {{.ExpiresAt}}.</p>
<ol>
{{if .Trust}}<li id="trust"><a class="button" href="{{.TrustPath}}">Download the server trust profile</a><br>Install it first in System Settings, then return to this page.</li>
{{end}}<li id="enroll"><a class="button" href="{{.Profile}}">Download the enrollment profile</a><br>Then open System Settings, review the downloaded profile and install it.</li>
</ol>
</body>
</html>
`))
