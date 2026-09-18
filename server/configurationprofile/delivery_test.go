package configurationprofile_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/url"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// Configuration profiles must work with ordinary DDM declarations, without
// requiring a Blueprint or a declaration identifier owned by its compiler.
func TestDeliveryFromDeclarations(t *testing.T) {
	for _, delivery := range []string{"ProfileURL", "ProfileAssetReference"} {
		t.Run(delivery, func(t *testing.T) {
			ctx := t.Context()
			e, err := ddm.New(ddm.Config{Store: inmem.New(), Expander: configurationprofile.Expander{BaseURL: "https://mdm.example"}})
			if err != nil {
				t.Fatal(err)
			}
			m, err := configurationprofile.New(configurationprofile.Config{Engine: e, State: state.NewMemory(), BaseURL: "https://mdm.example"})
			if err != nil {
				t.Fatal(err)
			}
			p := &profile.Profile{Identifier: "com.example.settings", UUID: "6C9B0C20-0000-7000-8000-000000000001", Scope: profile.ScopeUser, Payloads: []profile.Payload{{Identifier: "com.example.settings.payload", UUID: "6C9B0C20-0000-7000-8000-000000000002", Content: &profile.Raw{Type: "com.example.custom", Keys: map[string]any{"Value": "secret"}}}}}
			data, err := p.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			info, err := m.Upload(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			if info.PayloadIdentifier != p.Identifier || info.PayloadUUID != p.UUID {
				t.Fatal("configuration profile identity changed", info)
			}
			publication := ddm.SetPublication{Name: "configuration-profiles"}
			add := func(identifier, typ string, payload any) {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"Identifier": identifier, "Type": typ, "Payload": payload})
				if err != nil {
					t.Fatal(err)
				}
				publication.Declarations = append(publication.Declarations, jsontext.Value(raw))
			}
			ref := m.ProfileURL(info.Revision)
			if delivery == "ProfileAssetReference" {
				add("data", "com.apple.asset.data", map[string]any{"Reference": map[string]any{"DataURL": ref, "ContentType": info.ContentType, "Size": info.Size, "Hash-SHA-256": info.Revision}, "Authentication": map[string]any{"Type": "MDM"}})
				ref = "data"
			}
			add("settings", "com.apple.configuration.legacy", map[string]any{delivery: ref})
			add("activation", "com.apple.activation.simple", map[string]any{"StandardConfigurations": []string{"settings"}})
			if _, err := e.PublishSet(ctx, publication); err != nil {
				t.Fatal(err)
			}
			id := mdm.EnrollmentID{ID: "device:alice", ParentID: "device", Channel: mdm.ChannelUserEnrollmentUser}
			if _, err := e.AssignSet(ctx, id, publication.Name); err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.Fetch(ctx, id, info.Revision); !errors.Is(err, ddm.ErrNotFound) {
				t.Fatal("unadvertised profile accessible", err)
			}
			if _, err := e.Manifest(ctx, id); err != nil {
				t.Fatal(err)
			}
			dataID, field := "settings", "ProfileURL"
			if delivery == "ProfileAssetReference" {
				dataID, field = "data", "DataURL"
			}
			snapshot, err := e.Store().Snapshot(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range snapshot.Items {
				if item.Identifier != dataID {
					continue
				}
				var wire struct{ Payload map[string]any }
				if err := json.Unmarshal(item.Expanded, &wire); err != nil {
					t.Fatal(err)
				}
				payload := wire.Payload
				if delivery == "ProfileAssetReference" {
					var ok bool
					payload, ok = payload["Reference"].(map[string]any)
					if !ok {
						t.Fatal("missing asset reference")
					}
				}
				raw, ok := payload[field].(string)
				if !ok {
					t.Fatal("missing download URL")
				}
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				q := u.Query()
				if q.Get("id") != id.ID || q.Get("parent") != id.ParentID || q.Get("channel") != id.Channel.String() {
					t.Fatal("incomplete identity", raw)
				}
				found = true
			}
			if !found {
				t.Fatal("download declaration absent")
			}
			got, _, err := m.Fetch(ctx, id, info.Revision)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("download", err)
			}
			if _, err := e.UnassignSet(ctx, id, publication.Name); err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.Fetch(ctx, id, info.Revision); !errors.Is(err, ddm.ErrNotFound) {
				t.Fatal("removed assignment retained access", err)
			}
		})
	}
}
