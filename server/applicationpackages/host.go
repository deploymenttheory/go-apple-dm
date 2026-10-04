package applicationpackages

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const grantPrefix = "applications/download-grants/"

// Path is the public download prefix. Redact the next path component before logs.
const Path = "/applications/download/"

// Config separates verified content from enrollment authorization and dispatch.
type Config struct {
	Packages  *applications.Manager
	State     state.Store
	BaseURL   string
	Now       func() time.Time
	Target    func(context.Context, mdm.EnrollmentID) (support.Target, error)
	Authorize func(context.Context, mdm.EnrollmentID) error
}

// Host prepares device-specific delivery and authorizes immutable downloads.
type Host struct{ cfg Config }

// Grant describes one expiring download capability. Its bearer token is never
// retained; ID is the token hash used for explicit revocation by the operator.
type Grant struct {
	ID              string           `json:"id"`
	Enrollment      mdm.EnrollmentID `json:"enrollment"`
	PackageID       string           `json:"packageId"`
	ContentRevision string           `json:"contentRevision"`
	Method          string           `json:"method"`
	ExpiresAt       time.Time        `json:"expiresAt"`
}

// Plan contains the exact protocol payload to dispatch after preparation. The
// server returns Grant and dispatch IDs to operators; URLs and payloads are secrets.
type Plan struct {
	Grant       Grant
	ManifestURL string
	Command     *mdm.Command
	Publication ddm.SetPublication
}

// New validates the HTTPS hosting origin and required enrollment hooks.
func New(cfg Config) (*Host, error) {
	if cfg.Packages == nil || cfg.State == nil || cfg.Target == nil || cfg.Authorize == nil {
		return nil, applications.ErrInvalid
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return nil, applications.ErrInvalid
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Host{cfg: cfg}, nil
}

// Prepare validates metadata and Apple's platform rules before creating a grant.
// method is mdm or ddm. TTL defaults to one hour and may not exceed seven days.
// DDM requests a required install; optional self-service policy is not implied.
func (h *Host) Prepare(ctx context.Context, id mdm.EnrollmentID, packageID, revision, method string, ttl time.Duration) (Plan, error) {
	var plan Plan
	if id.Validate() != nil || id.Channel != mdm.ChannelDevice || (method != "mdm" && method != "ddm") || ttl < 0 || ttl > 7*24*time.Hour {
		return plan, applications.ErrInvalid
	}
	if ttl == 0 {
		ttl = time.Hour
	}
	if err := h.cfg.Authorize(ctx, id); err != nil {
		return plan, err
	}
	c, err := h.cfg.Packages.Revision(ctx, packageID, revision)
	if err != nil {
		return plan, err
	}
	target, err := h.cfg.Target(ctx, id)
	if err != nil {
		return plan, err
	}
	if target.OS != support.MacOS || target.Version.IsZero() {
		return plan, applications.ErrIneligible
	}
	if err = c.Metadata.CheckNativeDelivery(target.Version.String()); err != nil {
		return plan, err
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return plan, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	hash := tokenHash(token)
	plan.Grant = Grant{ID: hash, Enrollment: id, PackageID: packageID, ContentRevision: revision, Method: method}
	plan.ManifestURL = h.cfg.BaseURL + Path + token + "/manifest.plist"
	if method == "mdm" {
		payload := &commands.InstallEnterpriseApplication{ManifestURL: &plan.ManifestURL}
		if err = payload.Validate(target); err != nil {
			return Plan{}, fmt.Errorf("%w: %w", applications.ErrIneligible, err)
		}
		plan.Command, err = mdm.NewCommand(payload)
		if err != nil {
			return Plan{}, err
		}
	} else {
		required := "Required"
		payload := &schema.Package{ManifestURL: plan.ManifestURL, InstallBehavior: &schema.PackageInstallBehavior{Install: &required}}
		if err = payload.Validate(target); err != nil {
			return Plan{}, fmt.Errorf("%w: %w", applications.ErrIneligible, err)
		}
		// Keep both identifiers within Apple's 64-byte declaration limit.
		identifier := "pkg." + hash[:48]
		configuration, err := json.Marshal(map[string]any{"Identifier": identifier, "Type": schema.DeclarationTypePackage, "Payload": payload})
		if err != nil {
			return Plan{}, err
		}
		activation, err := json.Marshal(map[string]any{"Identifier": identifier + ".activation", "Type": schema.DeclarationTypeActivationSimple, "Payload": map[string]any{"StandardConfigurations": []string{identifier}}})
		if err != nil {
			return Plan{}, err
		}
		plan.Publication = ddm.SetPublication{Name: identifier, Declarations: []jsontext.Value{configuration, activation}}
	}
	err = h.cfg.State.Update(ctx, []string{grantPrefix + hash}, func(tx state.Tx) error {
		plan.Grant.ExpiresAt = tx.Now().Add(ttl)
		b, e := json.Marshal(plan.Grant)
		if e != nil {
			return e
		}
		return tx.Put(ctx, state.Record{Key: grantPrefix + hash, Value: b, ExpiresAt: plan.Grant.ExpiresAt})
	})
	return plan, err
}

// tokenHash names a capability without persisting its bearer value.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// grant resolves a live bearer grant and verifies enrollment and content liveness.
func (h *Host) grant(ctx context.Context, token string) (Grant, applications.Content, error) {
	var g Grant
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return g, applications.Content{}, applications.ErrNotFound
	}
	row, err := h.cfg.State.Get(ctx, grantPrefix+tokenHash(token))
	if errors.Is(err, state.ErrNotFound) {
		return g, applications.Content{}, applications.ErrNotFound
	}
	if err != nil {
		return g, applications.Content{}, err
	}
	if err = json.Unmarshal(row.Value, &g); err != nil {
		return g, applications.Content{}, err
	}
	if g.ID != tokenHash(token) || !h.cfg.Now().Before(g.ExpiresAt) {
		return g, applications.Content{}, applications.ErrNotFound
	}
	if err = h.cfg.Authorize(ctx, g.Enrollment); err != nil {
		return g, applications.Content{}, err
	}
	content, err := h.cfg.Packages.Revision(ctx, g.PackageID, g.ContentRevision)
	return g, content, err
}

// Manifest returns the library-generated plist for the grant's immutable revision.
func (h *Host) Manifest(ctx context.Context, token string) ([]byte, Grant, error) {
	grant, content, err := h.grant(ctx, token)
	if err != nil {
		return nil, grant, err
	}
	b, err := applications.BuildManifest(content, h.cfg.BaseURL+Path+token+"/package.pkg")
	return b, grant, err
}

// Content returns immutable metadata after authenticating the download grant.
func (h *Host) Content(ctx context.Context, token string) (applications.Content, Grant, error) {
	g, c, err := h.grant(ctx, token)
	return c, g, err
}

// Open checks grant liveness and opens the selected bounded package range.
func (h *Host) Open(ctx context.Context, token string, offset, length int64) (io.ReadCloser, applications.Content, error) {
	grant, content, err := h.grant(ctx, token)
	if err != nil {
		return nil, content, err
	}
	return h.cfg.Packages.Open(ctx, grant.PackageID, grant.ContentRevision, offset, length)
}

// Revoke removes one capability only when it belongs to the addressed enrollment.
// Revocation is idempotent after the grant expires or has already been removed.
func (h *Host) Revoke(ctx context.Context, id mdm.EnrollmentID, grantID string) error {
	decoded, err := hex.DecodeString(grantID)
	if err != nil || len(decoded) != 32 || id.Validate() != nil {
		return applications.ErrInvalid
	}
	key := grantPrefix + grantID
	return h.cfg.State.Update(ctx, []string{key}, func(tx state.Tx) error {
		row, e := tx.Get(ctx, key)
		if errors.Is(e, state.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		var grant Grant
		if e = json.Unmarshal(row.Value, &grant); e != nil {
			return e
		}
		if grant.Enrollment != id {
			return applications.ErrNotFound
		}
		return tx.Delete(ctx, key)
	})
}
