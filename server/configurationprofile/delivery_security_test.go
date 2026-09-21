package configurationprofile_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// profileDeclaration encodes a legacy-profile declaration referencing the supplied URL.
func profileDeclaration(t *testing.T, location string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"Identifier": "settings", "Type": schema.DeclarationTypeLegacyProfile, "Payload": map[string]any{"ProfileURL": location}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// advertiseProfile uploads a profile, publishes and assigns its declaration, and builds the
// enrollment's advertised snapshot.
func advertiseProfile(t *testing.T, cfg configurationprofile.Config, id mdm.EnrollmentID, scope string) (configurationprofile.Info, []byte) {
	t.Helper()
	m := profileManager(t, cfg)
	data := marshalProfile(t, testProfile(scope, "test"))
	info, err := m.Upload(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Engine.PublishSet(t.Context(), ddm.SetPublication{Name: "profiles", Declarations: []jsontext.Value{
		profileDeclaration(t, m.ProfileURL(info.Revision)),
		jsontext.Value(`{"Identifier":"activate","Type":"com.apple.activation.simple","Payload":{"StandardConfigurations":["settings"]}}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Engine.AssignSet(t.Context(), id, "profiles"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Engine.Manifest(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	return info, data
}

// TestFetchEnforcesConfigurationProfileScope checks that fetch enforces configuration profile
// scope.
func TestFetchEnforcesConfigurationProfileScope(t *testing.T) {
	for _, tc := range []struct {
		name, scope string
		channel     support.Channel
		allowed     bool
	}{
		{"system on device", profile.ScopeSystem, support.ChannelDevice, true},
		{"default on device", "", support.ChannelDevice, true},
		{"user on user", profile.ScopeUser, support.ChannelUser, true},
		{"system on user", profile.ScopeSystem, support.ChannelUser, false},
		{"default on user", "", support.ChannelUser, false},
		{"user on device", profile.ScopeUser, support.ChannelDevice, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := profileConfig(t)
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			if tc.channel == support.ChannelUser {
				id = mdm.EnrollmentID{ID: "device:alice", ParentID: "device", Channel: mdm.ChannelUserEnrollmentUser}
			}
			cfg.Target = func(_ context.Context, got mdm.EnrollmentID) (support.Target, error) {
				if got != id {
					t.Fatalf("target resolved for wrong identity: %+v", got)
				}
				return support.Target{OS: support.MacOS, Version: osversion.New(26, 0, 0), Channel: tc.channel, Supervised: true}, nil
			}
			info, original := advertiseProfile(t, cfg, id, tc.scope)
			data, got, err := profileManager(t, cfg).Fetch(t.Context(), id, info.Revision)
			if tc.allowed {
				if err != nil || got != info || !bytes.Equal(data, original) {
					t.Fatal("matching profile scope rejected", got, err)
				}
			} else if !errors.Is(err, ddm.ErrNotFound) || data != nil {
				t.Fatal("profile delivered on the wrong channel", err)
			}
		})
	}
}

// TestFetchRejectsInvalidAdvertisedDeclarations checks that fetch rejects invalid advertised
// declarations.
func TestFetchRejectsInvalidAdvertisedDeclarations(t *testing.T) {
	for _, change := range []string{"different id", "different channel", "different parent", "different revision", "external URL", "URL fragment", "malformed JSON", "missing version"} {
		t.Run(change, func(t *testing.T) {
			cfg := profileConfig(t)
			id := mdm.EnrollmentID{ID: "device:alice", ParentID: "device", Channel: mdm.ChannelUserEnrollmentUser}
			info, _ := advertiseProfile(t, cfg, id, profile.ScopeUser)
			m := profileManager(t, cfg)
			snapshot, err := cfg.Engine.Store().Snapshot(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(m.ProfileURL(info.Revision))
			if err != nil {
				t.Fatal(err)
			}
			q := url.Values{"id": {id.ID}, "channel": {id.Channel.String()}, "parent": {id.ParentID}}
			switch change {
			case "different id":
				q.Set("id", "device:bob")
			case "different channel":
				q.Set("channel", mdm.ChannelDevice.String())
			case "different parent":
				q.Set("parent", "other-device")
			case "different revision":
				u.Path = configurationprofile.Path + strings.Repeat("a", 64)
			case "external URL":
				u.Host = "other.example"
			case "URL fragment":
				u.Fragment = "fragment"
			}
			u.RawQuery = q.Encode()
			found := false
			for i := range snapshot.Items {
				item := &snapshot.Items[i]
				if item.Identifier != "settings" {
					continue
				}
				found = true
				item.Expanded = profileDeclaration(t, u.String())
				switch change {
				case "malformed JSON":
					item.Expanded = []byte(`{`)
				case "missing version":
					item.Expanded = nil
					item.BaseToken = "missing"
				}
			}
			if !found {
				t.Fatal("profile declaration not advertised")
			}
			if err := cfg.Engine.Store().PutSnapshot(t.Context(), snapshot); err != nil {
				t.Fatal(err)
			}
			if data, _, err := m.Fetch(t.Context(), id, info.Revision); !errors.Is(err, ddm.ErrNotFound) || data != nil {
				t.Fatal("invalid advertised declaration authorized a download", err)
			}
		})
	}
}

type failingDeliveryStore struct {
	ddm.Store
	updateErr, snapshotErr error
}

// Update injects a DDM update failure or delegates to the store.
func (s failingDeliveryStore) Update(ctx context.Context, fn func(ddm.Tx) error) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.Store.Update(ctx, fn)
}

// Snapshot injects a snapshot-read failure or delegates to the store.
func (s failingDeliveryStore) Snapshot(ctx context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	return s.Store.Snapshot(ctx, id)
}

// TestFetchPropagatesLookupFailures checks that fetch propagates lookup failures.
func TestFetchPropagatesLookupFailures(t *testing.T) {
	for _, operation := range []string{"compatibility", "snapshot", "target", "profile data"} {
		t.Run(operation, func(t *testing.T) {
			cfg := profileConfig(t)
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			info, _ := advertiseProfile(t, cfg, id, profile.ScopeSystem)
			failure := errors.New("injected " + operation + " failure")
			store := failingDeliveryStore{Store: cfg.Engine.Store()}
			switch operation {
			case "compatibility":
				store.updateErr = failure
			case "snapshot":
				store.snapshotErr = failure
			case "target":
				cfg.Target = func(context.Context, mdm.EnrollmentID) (support.Target, error) { return support.Target{}, failure }
			case "profile data":
				cfg.State = failingProfileState{Store: cfg.State, getErr: failure, getKey: "configuration-profile/data/"}
			}
			var err error
			cfg.Engine, err = ddm.New(ddm.Config{Store: store})
			if err != nil {
				t.Fatal(err)
			}
			if data, _, err := profileManager(t, cfg).Fetch(t.Context(), id, info.Revision); !errors.Is(err, failure) || data != nil {
				t.Fatal("lookup failure ignored", err)
			}
		})
	}
	m := profileManager(t, profileConfig(t))
	for _, tc := range []struct {
		id       mdm.EnrollmentID
		revision string
	}{
		{mdm.EnrollmentID{}, strings.Repeat("a", 64)},
		{mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}, "invalid"},
	} {
		if data, _, err := m.Fetch(t.Context(), tc.id, tc.revision); !errors.Is(err, ddm.ErrInvalid) || data != nil {
			t.Fatal("invalid download request accepted", err)
		}
	}
}

// TestExpanderOnlyBindsLocalProfileURLs checks that expander only binds local profile URLs.
func TestExpanderOnlyBindsLocalProfileURLs(t *testing.T) {
	expander := configurationprofile.Expander{BaseURL: "https://mdm.example/"}
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	local := "https://mdm.example" + configurationprofile.Path + strings.Repeat("a", 64)
	for _, rawURL := range []string{local, "https://external.example/profile", local + "?token=unrelated", local + "#fragment", "https://%/invalid", local + "/extra"} {
		t.Run(rawURL, func(t *testing.T) {
			raw := profileDeclaration(t, rawURL)
			original := bytes.Clone(raw)
			got, err := expander.Expand(t.Context(), id, &ddm.Declaration{Canonical: raw})
			if err != nil || !bytes.Equal(raw, original) {
				t.Fatal("expansion failed or changed stored bytes", err)
			}
			if rawURL != local {
				if got != nil {
					t.Fatal("rewrote a URL outside the hosted profile contract", string(got))
				}
				return
			}
			var envelope struct{ Payload struct{ ProfileURL string } }
			if err := json.Unmarshal(got, &envelope); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(envelope.Payload.ProfileURL)
			if err != nil {
				t.Fatal(err)
			}
			if u.Query().Get("id") != id.ID || u.Query().Get("channel") != id.Channel.String() || u.Query().Has("parent") {
				t.Fatal("device identity not bound correctly", u)
			}
		})
	}
	if data, err := expander.Expand(t.Context(), id, &ddm.Declaration{Canonical: []byte(`{`)}); err == nil || data != nil {
		t.Fatal("invalid declaration JSON accepted", err)
	}
}
