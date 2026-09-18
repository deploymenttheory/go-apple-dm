package configurationprofile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile/inspect"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const (
	MaxBytes          = 4 << 20
	Path              = "/configuration-profiles/"
	profilePrefix     = "configuration-profile/info/"
	profileDataPrefix = "configuration-profile/data/"
)

// Info describes an immutable upload. PayloadIdentifier and PayloadUUID retain
// Apple's profile envelope names; Revision is the server's SHA-256 storage key.
type Info struct {
	Revision, PayloadIdentifier, PayloadUUID, ContentType string
	Size                                                  int64
	Signed                                                bool
}

// ValidRevision reports whether a revision is a lowercase SHA-256 digest.
func ValidRevision(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateProfile(data []byte, target support.Target) (*profile.Parsed, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, ddm.ErrInvalid
	}
	report := inspect.Inspect(data, inspect.Options{Target: target})
	for _, issue := range report.Issues {
		if issue.Severity == "error" || issue.Rule == "encrypted" {
			return nil, fmt.Errorf("%w: profile %s: %s", ddm.ErrInvalid, issue.Rule, issue.Message)
		}
	}
	p, err := profile.Parse(data, profile.ParseOptions{MaxBytes: MaxBytes})
	if err != nil || p.Profile.Service != nil {
		return nil, fmt.Errorf("%w: invalid configuration profile", ddm.ErrInvalid)
	}
	if target.Channel != "" {
		scope := p.Profile.Scope
		if scope == "" {
			scope = profile.ScopeSystem
		}
		if (target.Channel == support.ChannelUser) != (scope == profile.ScopeUser) {
			return nil, fmt.Errorf("%w: profile scope does not match the enrollment channel", ddm.ErrInvalid)
		}
	}
	for _, payload := range p.Profile.Payloads {
		switch payload.Content.PayloadTypeName() {
		case "com.apple.mdm", "com.apple.declarations":
			return nil, fmt.Errorf("%w: enrollment and declaration payloads cannot be embedded in LegacyProfile", ddm.ErrInvalid)
		}
	}
	return p, nil
}

// Upload preserves the exact original bytes, payload identifiers and CMS
// signature. Re-uploading identical bytes returns the same immutable revision.
func (m *Manager) Upload(ctx context.Context, data []byte) (Info, error) {
	p, err := validateProfile(data, support.Target{})
	if err != nil {
		return Info{}, err
	}
	info := Info{Revision: hash(data), PayloadIdentifier: p.Profile.Identifier, PayloadUUID: p.Profile.UUID, Size: int64(len(data)), Signed: p.Signer != nil, ContentType: "application/xml"}
	if bytes.HasPrefix(data, []byte("bplist00")) {
		info.ContentType = "application/x-plist"
	}
	if info.Signed {
		info.ContentType = "application/x-apple-aspen-config"
	}
	err = m.cfg.State.Update(ctx, []string{profilePrefix + info.Revision}, func(tx state.Tx) error {
		if _, err := tx.Get(ctx, profilePrefix+info.Revision); err == nil {
			return nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err := tx.Put(ctx, state.Record{Key: profileDataPrefix + info.Revision, Value: data}); err != nil {
			return err
		}
		return put(ctx, tx, profilePrefix+info.Revision, info)
	})
	if err != nil {
		return Info{}, err
	}
	return info, nil
}

func (m *Manager) Get(ctx context.Context, revision string) (Info, error) {
	if !ValidRevision(revision) {
		return Info{}, ddm.ErrInvalid
	}
	return read[Info](ctx, m.cfg.State, profilePrefix+revision)
}

func (m *Manager) List(ctx context.Context, page paging.Page) (paging.Result[Info], error) {
	return list[Info](ctx, m.cfg.State, profilePrefix, page)
}

// ProfileURL returns the HTTPS location used by LegacyProfile.ProfileURL or
// AssetData.Reference.DataURL. An empty base URL disables hosted delivery.
func (m *Manager) ProfileURL(revision string) string {
	if m.cfg.BaseURL == "" || !ValidRevision(revision) {
		return ""
	}
	return m.cfg.BaseURL + Path + revision
}

func (m *Manager) Data(ctx context.Context, revision string) ([]byte, Info, error) {
	info, err := m.Get(ctx, revision)
	if err != nil {
		return nil, info, err
	}
	r, err := m.cfg.State.Get(ctx, profileDataPrefix+revision)
	if err != nil {
		return nil, info, err
	}
	if hash(r.Value) != revision {
		return nil, info, fmt.Errorf("configurationprofile: integrity failure")
	}
	return r.Value, info, nil
}

func profileURL(raw, base string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	prefix := base + Path
	if len(raw) != len(prefix)+64 || raw[:len(prefix)] != prefix {
		return "", false
	}
	rev := raw[len(prefix):]
	return rev, ValidRevision(rev)
}
