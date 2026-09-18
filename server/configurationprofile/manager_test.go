package configurationprofile_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

func profileConfig(t *testing.T) configurationprofile.Config {
	t.Helper()
	e, err := ddm.New(ddm.Config{Store: inmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	return configurationprofile.Config{Engine: e, State: state.NewMemory(), BaseURL: "https://mdm.example"}
}

func profileManager(t *testing.T, cfg configurationprofile.Config) *configurationprofile.Manager {
	t.Helper()
	m, err := configurationprofile.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func testProfile(scope, setting string) *profile.Profile {
	return &profile.Profile{Identifier: "com.example.settings", UUID: "6C9B0C20-0000-7000-8000-000000000001", Scope: scope, Payloads: []profile.Payload{{Identifier: "com.example.settings.payload", UUID: "6C9B0C20-0000-7000-8000-000000000002", Content: &profile.Raw{Type: "com.example.settings", Keys: map[string]any{"Value": setting}}}}}
}

func marshalProfile(t *testing.T, p *profile.Profile) []byte {
	t.Helper()
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestUploadPreservesFormatIdentityAndBytes(t *testing.T) {
	p := testProfile(profile.ScopeSystem, "test")
	ca, err := testpki.NewCA("configuration profile signer")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := p.Sign(ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Generated from the same envelope using Python plistlib.FMT_BINARY.
	binary, err := os.ReadFile("testdata/settings.binary.plist")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mediaType string
		data            []byte
		signed          bool
	}{
		{"XML", "application/xml", marshalProfile(t, p), false},
		{"binary plist", "application/x-plist", binary, false},
		{"CMS signature", "application/x-apple-aspen-config", signed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := profileManager(t, profileConfig(t))
			info, err := m.Upload(t.Context(), tc.data)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(tc.data)
			if info.Revision != hex.EncodeToString(digest[:]) || info.ContentType != tc.mediaType || info.Signed != tc.signed || info.Size != int64(len(tc.data)) || info.PayloadIdentifier != p.Identifier || info.PayloadUUID != p.UUID {
				t.Fatal("incorrect upload metadata", info)
			}
			again, err := m.Upload(t.Context(), bytes.Clone(tc.data))
			if err != nil || again != info {
				t.Fatal("identical upload changed immutable metadata", again, err)
			}
			data, saved, err := m.Data(t.Context(), info.Revision)
			if err != nil || saved != info || !bytes.Equal(data, tc.data) {
				t.Fatal("stored profile differs from upload", saved, err)
			}
			page, err := m.List(t.Context(), paging.Page{})
			if err != nil || !slices.Equal(page.Items, []configurationprofile.Info{info}) || page.NextCursor != "" {
				t.Fatal("duplicate upload created another record", page, err)
			}
		})
	}
}

func TestUploadRejectsInvalidAndUnsupportedProfiles(t *testing.T) {
	p := testProfile(profile.ScopeSystem, "test")
	mdmProfile := testProfile(profile.ScopeSystem, "test")
	mdmProfile.Payloads[0].Content = &profiles.MDM{IdentityCertificateUUID: "6C9B0C20-0000-7000-8000-000000000003", Topic: "com.apple.mgmt.test", ServerURL: "https://mdm.example/mdm"}
	declarations := testProfile(profile.ScopeSystem, "test")
	declarations.Payloads[0].Content = &profiles.Declarations{Declarations: [][]byte{[]byte(`{"Identifier":"math","Type":"com.apple.configuration.math.settings","Payload":{}}`)}}
	service := &profile.Profile{Identifier: p.Identifier, UUID: p.UUID, Service: &profile.ProfileService{URL: "https://mdm.example/enroll", DeviceAttributes: []string{"UDID"}}}
	top, err := p.Map()
	if err != nil {
		t.Fatal(err)
	}
	delete(top, "PayloadContent")
	top["EncryptedPayloadContent"] = []byte("encrypted fixture")
	encrypted, err := plist.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"malformed", []byte("not a profile")},
		{"oversized", bytes.Repeat([]byte("x"), configurationprofile.MaxBytes+1)},
		{"MDM payload", marshalProfile(t, mdmProfile)},
		{"declarations payload", marshalProfile(t, declarations)},
		{"Profile Service", marshalProfile(t, service)},
		{"encrypted payloads", encrypted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := profileConfig(t)
			m := profileManager(t, cfg)
			info, err := m.Upload(t.Context(), tc.data)
			if !errors.Is(err, ddm.ErrInvalid) || info.Revision != "" {
				t.Fatalf("Upload = %+v, %v; want rejected profile", info, err)
			}
			if (tc.name == "MDM payload" || tc.name == "declarations payload") && !strings.Contains(err.Error(), "cannot be embedded in LegacyProfile") {
				t.Fatal("did not enforce the LegacyProfile payload restriction", err)
			}
			rows, err := cfg.State.List(t.Context(), "", "", 10)
			if err != nil || len(rows) != 0 {
				t.Fatal("rejected upload wrote data or metadata", rows, err)
			}
		})
	}
}

func TestProfileRevisionValidationAndPagination(t *testing.T) {
	cfg := profileConfig(t)
	for _, bad := range []configurationprofile.Config{{}, {State: cfg.State}, {Engine: cfg.Engine}} {
		if m, err := configurationprofile.New(bad); m != nil || !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("New = %v, %v", m, err)
		}
	}
	cfg.BaseURL += "///"
	m := profileManager(t, cfg)
	for _, invalid := range []string{"", "../profile", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		if configurationprofile.ValidRevision(invalid) || m.ProfileURL(invalid) != "" {
			t.Fatal("accepted invalid revision", invalid)
		}
		if _, err := m.Get(t.Context(), invalid); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatal(err)
		}
	}
	var revisions []string
	for _, value := range []string{"one", "two", "three"} {
		info, err := m.Upload(t.Context(), marshalProfile(t, testProfile("", value)))
		if err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, info.Revision)
		if m.ProfileURL(info.Revision) != "https://mdm.example/configuration-profiles/"+info.Revision {
			t.Fatal("incorrect hosted URL", m.ProfileURL(info.Revision))
		}
	}
	slices.Sort(revisions)
	cursor := ""
	for i, revision := range revisions {
		page, err := m.List(t.Context(), paging.Page{Limit: 1, Cursor: cursor})
		if err != nil || len(page.Items) != 1 || page.Items[0].Revision != revision {
			t.Fatal("page order or cursor", page, err)
		}
		cursor = page.NextCursor
		if (i == len(revisions)-1) != (cursor == "") {
			t.Fatal("incorrect next cursor", cursor)
		}
	}
	cfg.BaseURL = ""
	if profileManager(t, cfg).ProfileURL(revisions[0]) != "" {
		t.Fatal("hosted delivery enabled without a public URL")
	}
	if _, _, err := m.Data(t.Context(), strings.Repeat("a", 64)); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("missing profile", err)
	}
}

type failingProfileState struct {
	state.Store
	getKey, putKey          string
	getErr, putErr, listErr error
}

func (s failingProfileState) Get(ctx context.Context, key string) (state.Record, error) {
	if s.getErr != nil && strings.HasPrefix(key, s.getKey) {
		return state.Record{}, s.getErr
	}
	return s.Store.Get(ctx, key)
}

func (s failingProfileState) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.List(ctx, prefix, after, limit)
}

func (s failingProfileState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(failingProfileTx{Tx: tx, faults: s}) })
}

type failingProfileTx struct {
	state.Tx
	faults failingProfileState
}

func (tx failingProfileTx) Get(ctx context.Context, key string) (state.Record, error) {
	if tx.faults.getErr != nil && strings.HasPrefix(key, tx.faults.getKey) {
		return state.Record{}, tx.faults.getErr
	}
	return tx.Tx.Get(ctx, key)
}

func (tx failingProfileTx) Put(ctx context.Context, r state.Record) error {
	if tx.faults.putErr != nil && strings.HasPrefix(r.Key, tx.faults.putKey) {
		return tx.faults.putErr
	}
	return tx.Tx.Put(ctx, r)
}

func TestUploadStorageFailuresAreAtomic(t *testing.T) {
	for _, operation := range []string{"read metadata", "write data", "write metadata"} {
		t.Run(operation, func(t *testing.T) {
			cfg := profileConfig(t)
			store := cfg.State
			failure := errors.New("injected storage error")
			faults := failingProfileState{Store: store}
			switch operation {
			case "read metadata":
				faults.getErr = failure
			case "write data":
				faults.putErr, faults.putKey = failure, "configuration-profile/data/"
			case "write metadata":
				faults.putErr, faults.putKey = failure, "configuration-profile/info/"
			}
			cfg.State = faults
			m := profileManager(t, cfg)
			if info, err := m.Upload(t.Context(), marshalProfile(t, testProfile("", "test"))); !errors.Is(err, failure) || info.Revision != "" {
				t.Fatal("upload ignored storage failure", info, err)
			}
			rows, err := store.List(t.Context(), "", "", 10)
			if err != nil || len(rows) != 0 {
				t.Fatal("failed upload left partial data or metadata", rows, err)
			}
		})
	}
}

func TestProfileReadFailures(t *testing.T) {
	for _, operation := range []string{"metadata error", "list error", "data error", "missing data", "corrupt metadata", "changed data"} {
		t.Run(operation, func(t *testing.T) {
			cfg := profileConfig(t)
			m := profileManager(t, cfg)
			info, err := m.Upload(t.Context(), marshalProfile(t, testProfile("", "test")))
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("storage unavailable")
			want := failure
			switch operation {
			case "metadata error":
				cfg.State = failingProfileState{Store: cfg.State, getErr: failure, getKey: "configuration-profile/info/"}
			case "list error":
				cfg.State = failingProfileState{Store: cfg.State, listErr: failure}
			case "data error":
				cfg.State = failingProfileState{Store: cfg.State, getErr: failure, getKey: "configuration-profile/data/"}
			default:
				want = nil
				if operation == "missing data" {
					want = state.ErrNotFound
				}
				err := cfg.State.Update(t.Context(), []string{"configuration-profile/info/" + info.Revision}, func(tx state.Tx) error {
					if operation == "missing data" {
						return tx.Delete(t.Context(), "configuration-profile/data/"+info.Revision)
					}
					prefix := "configuration-profile/data/"
					if operation == "corrupt metadata" {
						prefix = "configuration-profile/info/"
					}
					return tx.Put(t.Context(), state.Record{Key: prefix + info.Revision, Value: []byte("corrupt")})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			m = profileManager(t, cfg)
			if operation == "list error" || operation == "corrupt metadata" {
				if _, err := m.List(t.Context(), paging.Page{}); err == nil || (want != nil && !errors.Is(err, want)) {
					t.Fatal("list ignored storage error", err)
				}
			}
			if operation != "list error" {
				data, _, err := m.Data(t.Context(), info.Revision)
				if err == nil || data != nil || (want != nil && !errors.Is(err, want)) {
					t.Fatal("data read ignored corruption or storage failure", err)
				}
			}
		})
	}
}
