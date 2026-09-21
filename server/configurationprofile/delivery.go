package configurationprofile

import (
	"context"
	json "encoding/json/v2"
	"net/url"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// Expander binds locally hosted URLs to the complete enrollment identity.
// The URLs carry no bearer credential; downloads require the pinned MDM identity.
type Expander struct{ BaseURL string }

// Expand adds the complete enrollment identity to a locally hosted profile or data-asset
// URL. It returns (nil, nil) when no hosted URL applies; malformed declaration JSON returns
// an error. The URL contains no bearer credential.
func (e Expander) Expand(_ context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
	var wire struct {
		Identifier, Type string
		Payload          map[string]any
	}
	if err := json.Unmarshal(d.Canonical, &wire); err != nil {
		return nil, err
	}
	parent, key := urlField(wire.Type, wire.Payload)
	if parent == nil {
		return nil, nil
	}
	raw, _ := parent[key].(string)
	if _, ok := profileURL(raw, strings.TrimRight(e.BaseURL, "/")); !ok {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	q := url.Values{"id": {id.ID}, "channel": {channelName(id)}}
	if id.ParentID != "" {
		q.Set("parent", id.ParentID)
	}
	u.RawQuery = q.Encode()
	parent[key] = u.String()
	return json.Marshal(wire)
}

// channelName returns the wire name of the enrollment's management channel.
func channelName(id mdm.EnrollmentID) string {
	return id.Channel.String()
}

// urlField locates the URL field in a LegacyProfile or AssetData declaration payload.
func urlField(typ string, p map[string]any) (map[string]any, string) {
	switch typ {
	case schema.DeclarationTypeLegacyProfile:
		return p, "ProfileURL"
	case schema.DeclarationTypeAssetData:
		ref, _ := p["Reference"].(map[string]any)
		return ref, "DataURL"
	}
	return nil, ""
}

// Fetch requires both current eligibility and an advertised snapshot.
// An older advertised revision remains available after publication, but removal
// of its declaration or assignment immediately revokes access. Identity pinning
// and revocation checks belong to the authenticated MDM ingress.
func (m *Manager) Fetch(ctx context.Context, id mdm.EnrollmentID, revision string) ([]byte, Info, error) {
	if id.Validate() != nil || !ValidRevision(revision) {
		return nil, Info{}, ddm.ErrInvalid
	}
	report, err := m.cfg.Engine.Compatibility(ctx, id)
	if err != nil {
		return nil, Info{}, err
	}
	snapshot, err := m.cfg.Engine.Store().Snapshot(ctx, id)
	if err != nil {
		return nil, Info{}, err
	}
	eligible := map[string]bool{}
	for _, ref := range report.Eligible {
		eligible[ref.Identifier] = true
	}
	target := support.Target{}
	if m.cfg.Target != nil {
		target, err = m.cfg.Target(ctx, id)
		if err != nil {
			return nil, Info{}, err
		}
	}
	for _, item := range snapshot.Items {
		if !eligible[item.Identifier] {
			continue
		}
		raw := item.Expanded
		if len(raw) == 0 {
			version, err := m.cfg.Engine.Store().GetDeclarationVersion(ctx, item.Identifier, item.BaseToken)
			if err != nil {
				continue
			}
			raw = version.Canonical
		}
		decl, err := ddm.ParseDeclaration(raw, target)
		if err != nil {
			continue
		}
		var wire struct{ Payload map[string]any }
		if json.Unmarshal(decl.Canonical, &wire) != nil {
			continue
		}
		parent, key := urlField(decl.Type, wire.Payload)
		if parent == nil {
			continue
		}
		rawURL, _ := parent[key].(string)
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		// Expanded values must match this identity exactly.
		if u.RawQuery != "" {
			q := u.Query()
			if q.Get("id") != id.ID || q.Get("channel") != channelName(id) || q.Get("parent") != id.ParentID {
				continue
			}
		}
		u.RawQuery = ""
		ref, ok := profileURL(u.String(), m.cfg.BaseURL)
		if !ok || ref != revision {
			continue
		}
		data, info, err := m.Data(ctx, revision)
		if err != nil {
			return nil, info, err
		}
		if _, err := validateProfile(data, target); err != nil {
			return nil, Info{}, ddm.ErrNotFound
		}
		return data, info, nil
	}
	return nil, Info{}, ddm.ErrNotFound
}
