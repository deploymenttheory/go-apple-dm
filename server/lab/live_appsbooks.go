package lab

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// appsAcceptance is private operator intent. User licensing does not require a
// physical serial, ABM account, or device enrollment.
type appsAcceptance struct {
	Location  string          `json:"location"`
	UID       string          `json:"uid"`
	MDMID     string          `json:"mdm_id"`
	Asset     appsbooks.Asset `json:"asset"`
	Serial    string          `json:"serial,omitempty"`
	User      string          `json:"client_user_id,omitempty"`
	FreeApp   bool            `json:"designated_free_app"`
	Mutate    bool            `json:"allow_licensing"`
	Quiescent bool            `json:"exclusive_location_quiescent"`
	MaxPages  int             `json:"max_pages"`
}

type appsAccess struct {
	config        appsAcceptance
	client        *appsbooks.Client
	location      appsbooks.Location
	configuration appsbooks.ClientConfiguration
}

// appsNow returns fixture time only for simulated acceptance, otherwise wall time.
func appsNow(s *Session) time.Time {
	if s.Env.appsBooksFixture != nil && s.Env.appsBooksFixture.Clock != nil {
		return s.Env.appsBooksFixture.Clock.Now()
	}
	return time.Now()
}

// appsRead bounds private inputs and hides credential paths and contents.
func appsRead(s *Session, name string) ([]byte, error) {
	if s.Env == nil || s.Env.Workspace == nil {
		return nil, fmt.Errorf("%w: private Apps and Books workspace required", ErrBlocked)
	}
	f, err := os.Open(s.Env.Workspace.path("appsbooks", name)) // #nosec G304 -- Fixed internal filenames under the operator's private workspace.
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: dedicated location sToken or private acceptance scope missing", ErrBlocked)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: private Apps and Books input unavailable", errOperation)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return nil, fmt.Errorf("%w: private Apps and Books input unreadable or oversized", errOperation)
	}
	return b, nil
}

// appsConfig validates the explicit private location and bounded collection scope.
func appsConfig(s *Session) (appsAcceptance, error) {
	var c appsAcceptance
	b, err := appsRead(s, "acceptance.json")
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Location == "" || c.UID == "" || c.MDMID == "" || c.MaxPages < 1 || c.MaxPages > 1000 {
		return c, fmt.Errorf("%w: explicit location identity and bounded page scope required", ErrBlocked)
	}
	return c, nil
}

// appsError preserves cancellation while withholding private upstream errors.
func appsError(ctx context.Context, phase string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: Apps and Books %s interrupted", ctx.Err(), phase)
	}
	return fmt.Errorf("%w: Apps and Books %s failed", errOperation, phase)
}

// appsSDKError classifies missing Apple permissions without exposing raw responses.
func appsSDKError(ctx context.Context, phase string, cause error) error {
	var api *appsbooks.APIError
	if errors.Is(cause, appsbooks.ErrExpired) || errors.As(cause, &api) && (api.HTTPStatus == http.StatusForbidden || api.HTTPStatus == http.StatusUnauthorized) {
		return fmt.Errorf("%w: Apps and Books token or required location permission unavailable", ErrBlocked)
	}
	return appsError(ctx, phase)
}

// appsRequest uses the normal reference API and never exposes raw response errors.
func appsRequest(ctx context.Context, s *Session, method, path string, in, out any) error {
	var data []byte
	var err error
	if in != nil {
		data, err = jsonv2.Marshal(in)
		if err != nil {
			return appsError(ctx, "request encoding")
		}
	}
	b, status, err := HTTP(ctx, s.Env.Client, s.Env.URL, s.Env.Token, method, path, bytes.NewReader(data))
	if err != nil {
		if status == http.StatusNotFound || status == http.StatusForbidden || status == http.StatusUnauthorized {
			return fmt.Errorf("%w: reference location or required Apps and Books permission unavailable", ErrBlocked)
		}
		return appsError(ctx, "reference request")
	}
	if out != nil && jsonv2.Unmarshal(b, out) != nil {
		return appsError(ctx, "reference response")
	}
	return nil
}

// appsBase addresses the selected opaque reference location identifier.
func appsBase(c appsAcceptance) string {
	return "/appsbooks/locations/" + url.PathEscape(c.Location)
}

// appsScope creates an exclusive private evidence directory, including on reruns.
func appsScope(s *Session, phase string) (*Session, string, error) {
	if s.Evidence == "" {
		return nil, "", fmt.Errorf("%w: private Apps and Books evidence directory required", ErrBlocked)
	}
	parent, err := filepath.Abs(s.Evidence)
	if err != nil {
		return nil, "", appsError(context.Background(), "evidence path")
	}
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return nil, "", appsError(context.Background(), "evidence directory")
	}
	id := randomID()
	copy := *s
	copy.Evidence = filepath.Join(parent, "appsbooks-"+phase+"-"+id)
	if err = os.Mkdir(copy.Evidence, 0o700); err != nil {
		return nil, "", appsError(context.Background(), "exclusive evidence directory")
	}
	return &copy, id, nil
}

// appsSave creates exclusive private structured evidence without replacing prior files.
func appsSave(s *Session, name string, value any) error {
	b, err := jsonv2.Marshal(value)
	if err != nil {
		return appsError(context.Background(), "evidence encoding")
	}
	if err = privateFile(filepath.Join(s.Evidence, name), b); err != nil {
		return appsError(context.Background(), "exclusive evidence persistence")
	}
	return nil
}

// appsSaveRaw retains original source bytes, including whitespace, escape
// spelling and number tokens. It never reserializes Apple's JSON evidence.
func appsSaveRaw(s *Session, name string, data []byte) error {
	if !json.Valid(data) {
		return appsError(context.Background(), "source evidence validation")
	}
	if err := privateFile(filepath.Join(s.Evidence, name), data); err != nil {
		return appsError(context.Background(), "exclusive source evidence persistence")
	}
	return nil
}

// appsOpen never imports a token, configures callbacks, or claims a location.
func appsOpen(ctx context.Context, s *Session) (*appsAccess, error) {
	c, err := appsConfig(s)
	if err != nil {
		return nil, err
	}
	b, err := appsRead(s, "stoken")
	if err != nil {
		return nil, err
	}
	cfg := appsbooks.Config{SToken: strings.TrimSpace(string(b)), MDMID: c.MDMID, UID: c.UID}
	if s.Env.appsBooksFixture != nil {
		cfg.BaseURL, cfg.HTTPClient, cfg.Clock = s.Env.appsBooksFixture.BaseURL, s.Env.appsBooksFixture.HTTPClient, s.Env.appsBooksFixture.Clock
	}
	client, err := appsbooks.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: dedicated valid Apps and Books sToken required", ErrBlocked)
	}
	a := &appsAccess{config: c, client: client}
	if err = appsRequest(ctx, s, "GET", appsBase(c), nil, &a.location); err != nil {
		return nil, err
	}
	if a.location.ID != c.Location || a.location.UID != c.UID || a.location.MDMID != c.MDMID || a.location.Revision == 0 || a.location.ConfigurationPending {
		return nil, fmt.Errorf("%w: reference location binding is different or pending", ErrBlocked)
	}
	a.configuration, err = client.ClientConfig(ctx)
	if err != nil {
		return nil, appsSDKError(ctx, "location authentication", err)
	}
	if a.configuration.UID != c.UID {
		return nil, appsError(ctx, "location identity")
	}
	return a, nil
}

// appsRevision identifies the compiled source revision for acceptance evidence.
func appsRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "unavailable"
}

// liveAppsBooksCollection independently observes native API facts through the
// SDK. The reference location binding is a read, not registration or refresh.
func liveAppsBooksCollection(ctx context.Context, session *Session) (result error) {
	s, _, err := appsScope(session, "api")
	if err != nil {
		return err
	}
	checks := map[string]any{"source_revision": appsRevision(), "api": "unexercised", "delivery": "unexercised", "installation": "unexercised", "mutations": false, "mode": s.Env.Mode}
	defer func() { result = errors.Join(result, appsSave(s, "assertions.json", checks)) }()
	a, err := appsOpen(ctx, s)
	if err != nil {
		return err
	}
	service, err := a.client.ServiceConfig(ctx)
	if err != nil {
		return appsSDKError(ctx, "service configuration", err)
	}
	if err = appsSaveRaw(s, "configuration.json", a.configuration.Raw); err != nil {
		return err
	}
	if err = appsSave(s, "service.json", service); err != nil {
		return err
	}
	if err = appsSave(s, "reference-location.json", a.location); err != nil {
		return err
	}
	for _, kind := range []string{"assets", "users", "assignments"} {
		if err = a.pages(ctx, s, kind, nil); err != nil {
			return err
		}
	}
	checks["api"] = "passed"
	if s.Env.Mode == "simulated" {
		checks["api"] = "simulated"
	}
	checks["location_revision"] = a.location.Revision
	return nil
}

// pages preserves complete original bytes and rejects incoherent traversals.
// A target filter is supplied only for designated licensing checks.
func (a *appsAccess) pages(ctx context.Context, s *Session, kind string, assignments *[]appsbooks.Assignment) error {
	index := 0
	seen := map[int]bool{}
	version := ""
	total := -1
	for number := 0; number < a.config.MaxPages; number++ {
		var page appsbooks.Pagination
		var raw []byte
		var err error
		count := 0
		switch kind {
		case "assets":
			var p appsbooks.AssetsPage
			p, err = a.client.Assets(ctx, appsbooks.AssetsQuery{PageIndex: index})
			page, raw = p.Pagination, p.Raw
			count = len(p.Assets) + len(p.UnlimitedAssets)
		case "users":
			var p appsbooks.UsersPage
			p, err = a.client.Users(ctx, appsbooks.UsersQuery{PageIndex: index})
			page, raw = p.Pagination, p.Raw
			count = len(p.Users)
		case "assignments":
			q := appsbooks.AssignmentsQuery{PageIndex: index}
			if assignments != nil {
				q.AdamID, q.PricingParam, q.SerialNumber, q.ClientUserID = a.config.Asset.AdamID, a.config.Asset.PricingParam, a.config.Serial, a.config.User
				q.IncludeUserState = new(true)
			}
			var p appsbooks.AssignmentsPage
			p, err = a.client.Assignments(ctx, q)
			page, raw = p.Pagination, p.Raw
			count = len(p.Assignments)
			if err == nil && assignments != nil {
				*assignments = append(*assignments, p.Assignments...)
			}
		default:
			return appsError(ctx, "collection kind")
		}
		if err != nil {
			return appsSDKError(ctx, kind+" collection", err)
		}
		if version != "" && page.VersionID != version || page.VersionID == "" || seen[index] {
			return appsError(ctx, kind+" coherence")
		}
		version = page.VersionID
		seen[index] = true
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return appsError(ctx, kind+" source")
		}
		if value, ok := fields["totalPages"]; ok && string(value) != "null" {
			var n int
			if json.Unmarshal(value, &n) != nil || n < 0 || total >= 0 && n != total {
				return appsError(ctx, kind+" page total")
			}
			total = n
			if n == 0 && (index != 0 || page.NextPageIndex != nil || count != 0) || n > 0 && (index >= n || page.NextPageIndex == nil && index != n-1 || page.NextPageIndex != nil && *page.NextPageIndex >= n) {
				return appsError(ctx, kind+" incomplete traversal")
			}
		}
		if err = appsSaveRaw(s, fmt.Sprintf("%s-page-%04d.json", kind, number), raw); err != nil {
			return err
		}
		if page.NextPageIndex == nil {
			return nil
		}
		if *page.NextPageIndex != index+1 {
			return appsError(ctx, kind+" continuation")
		}
		index = *page.NextPageIndex
	}
	return appsError(ctx, kind+" page bound")
}
