package lab

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

// adeAcceptance names one server binding and physical device. Credentials are
// supplied separately in the private workspace's ade/tokens.json.
type adeAcceptance struct {
	Account    string `json:"account"`
	ServerUUID string `json:"server_uuid"`
	Serial     string `json:"serial"`
	Platform   string `json:"platform"`
	OSVersion  string `json:"os_version"`
	DeviceID   string `json:"device_id"`
	Identity   string `json:"identity"`
	Mutate     bool   `json:"allow_profile_assignment"`
	Quiescent  bool   `json:"exclusive_account_quiescent"`
	DDM        bool   `json:"exercise_ddm"`
}

type adeAccount struct {
	Name, ServerUUID string
	HasTokens        bool
	State            dep.AccountState
}

type adeSelection struct {
	Default  string            `json:"Default"`
	Serials  map[string]string `json:"Serials"`
	Revision int64             `json:"Revision"`
}

type adeAccess struct {
	config adeAcceptance
	client *dep.Client
	detail *dep.AccountDetail
}

// adeRead bounds private configuration input without including its content or
// credential path in public errors.
func adeRead(s *Session, name string) ([]byte, error) {
	if s.Env == nil || s.Env.Workspace == nil {
		return nil, fmt.Errorf("%w: private ADE workspace required", ErrBlocked)
	}
	b, err := os.ReadFile(s.Env.Workspace.path("ade", name)) // #nosec G304 -- Callers supply fixed filenames beneath the operator-owned private ADE workspace.
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: private ADE configuration or separate server token missing", ErrBlocked)
	}
	if err != nil || len(b) > 1<<20 {
		return nil, fmt.Errorf("%w: private ADE input unreadable or oversized", errOperation)
	}
	return b, nil
}

// adeConfig rejects incomplete or ambiguous operator scope before Apple requests.
func adeConfig(s *Session) (adeAcceptance, error) {
	var c adeAcceptance
	b, err := adeRead(s, "acceptance.json")
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Account == "" || c.ServerUUID == "" || inventory.CanonicalSerial(c.Serial) == "" {
		return c, fmt.Errorf("%w: ADE account, server binding and designated serial required", ErrBlocked)
	}
	c.Serial = inventory.CanonicalSerial(c.Serial)
	return c, nil
}

// adeError keeps remote bodies, private identifiers and OAuth request details out
// of public results. The original context remains observable on cancellation.
func adeError(ctx context.Context, operation string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: ADE %s interrupted", ctx.Err(), operation)
	}
	return fmt.Errorf("%w: ADE %s failed", errOperation, operation)
}

// adeRequest uses only the ordinary authenticated administration API. It does
// not expose response bodies, paths or designated identifiers in errors.
func adeRequest(ctx context.Context, s *Session, method, path string, in, out any) error {
	var body []byte
	var err error
	if in != nil {
		body, err = jsonv2.Marshal(in)
		if err != nil {
			return adeError(ctx, "request encoding")
		}
	}
	b, status, err := HTTP(ctx, s.Env.Client, s.Env.URL, s.Env.Token, method, path, bytes.NewReader(body))
	if err != nil {
		if status == http.StatusNotFound {
			return fmt.Errorf("%w: designated ADE prerequisite unavailable", ErrBlocked)
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return fmt.Errorf("%w: ADE administration permission denied", errOperation)
		}
		return adeError(ctx, "administration request")
	}
	if out != nil && jsonv2.Unmarshal(b, out) != nil {
		return adeError(ctx, "administration response")
	}
	return nil
}

// adeSave stores identifiable evidence only in the private run directory.
func adeSave(s *Session, name string, value any) error {
	b, err := jsonv2.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: ADE evidence encoding failed", errOperation)
	}
	if err = abmSave(s, name, b); err != nil {
		return fmt.Errorf("%w: private ADE evidence could not be retained", errOperation)
	}
	return nil
}

// adeScope creates an exclusive evidence scope for every invocation, including
// reruns beneath the same report directory.
func adeScope(s *Session, prefix string) (*Session, string, error) {
	if s.Evidence == "" {
		return nil, "", fmt.Errorf("%w: private ADE evidence directory required", ErrBlocked)
	}
	if err := os.MkdirAll(s.Evidence, 0o700); err != nil {
		return nil, "", fmt.Errorf("%w: private ADE evidence directory unavailable", errOperation)
	}
	id := randomID()
	copy := *s
	copy.Evidence = filepath.Join(s.Evidence, prefix+"-"+id)
	if err := os.Mkdir(copy.Evidence, 0o700); err != nil {
		return nil, "", fmt.Errorf("%w: exclusive ADE evidence directory unavailable", errOperation)
	}
	return &copy, id, nil
}

// adeBase addresses the configured reference account with an escaped name.
func adeBase(c adeAcceptance) string { return "/dep/accounts/" + url.PathEscape(c.Account) }

// adeReferenceAccount requires the selected SDK server identity to be the same
// configured ADE account used by the reference API. Opaque server UUIDs bind the
// protocol account; only the serial selects the physical device.
func adeReferenceAccount(ctx context.Context, s *Session, c adeAcceptance) error {
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; pages < 1000; pages++ {
		var page struct {
			Items      []adeAccount
			NextCursor string
		}
		if err := adeRequest(ctx, s, "GET", "/dep/accounts?cursor="+url.QueryEscape(cursor), nil, &page); err != nil {
			return err
		}
		for _, a := range page.Items {
			if a.Name != c.Account {
				continue
			}
			if !a.HasTokens {
				return fmt.Errorf("%w: selected reference ADE account has no server token", ErrBlocked)
			}
			if a.ServerUUID != c.ServerUUID {
				return fmt.Errorf("%w: reference and SDK ADE account bindings differ", errOperation)
			}
			if a.State.TermsExpired || a.State.TokenInvalid {
				return fmt.Errorf("%w: reference ADE account requires credential or terms action", ErrBlocked)
			}
			return nil
		}
		if page.NextCursor == "" {
			return fmt.Errorf("%w: selected reference ADE account missing", ErrBlocked)
		}
		if seen[page.NextCursor] {
			return adeError(ctx, "account pagination")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return adeError(ctx, "account pagination bound")
}

// adeOpen validates the separate OAuth token through the real shared library
// client. Its in-memory credential/session store is private to this invocation;
// reference profile selection and synchronization use the maintained server API.
func adeOpen(ctx context.Context, s *Session) (*adeAccess, error) {
	c, err := adeConfig(s)
	if err != nil {
		return nil, err
	}
	if err = adeReferenceAccount(ctx, s, c); err != nil {
		return nil, err
	}
	b, err := adeRead(s, "tokens.json")
	if err != nil {
		return nil, err
	}
	var tokens dep.Tokens
	if jsonv2.Unmarshal(b, &tokens) != nil || tokens.Validate() != nil {
		return nil, fmt.Errorf("%w: a separate valid ADE OAuth server token is required", ErrBlocked)
	}
	cfg := dep.ClientConfig{Store: depinmem.New(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// Embedded fixture tests use the same OAuth implementation with private TLS.
	// Normal live workspaces have no DEP fixture and use Apple's HTTPS origin.
	if s.Env.DEP != nil {
		cfg.BaseURL, cfg.HTTPClient = s.Env.DEP.URL(), s.Env.DEP.Client()
	}
	client, err := dep.NewClient(cfg)
	if err != nil {
		return nil, adeError(ctx, "client configuration")
	}
	detail, err := client.StoreTokens(ctx, c.Account, tokens)
	if err != nil {
		return nil, adeError(ctx, "token authentication")
	}
	if detail.ServerUUID != c.ServerUUID {
		return nil, fmt.Errorf("%w: separate ADE token belongs to another server", errOperation)
	}
	return &adeAccess{config: c, client: client, detail: detail}, nil
}

// adeDevice requires a successful, serial-consistent remote detail response.
func (a *adeAccess) device(ctx context.Context) (dep.Device, error) {
	rows, err := a.client.DeviceDetails(ctx, a.config.Account, []string{a.config.Serial})
	if err != nil {
		return dep.Device{}, adeError(ctx, "device readback")
	}
	d, ok := rows[a.config.Serial]
	if !ok || d.ResponseStatus == dep.StatusNotAccessible {
		return d, fmt.Errorf("%w: designated ADE device inaccessible or unexercised", ErrBlocked)
	}
	if d.ResponseStatus != dep.StatusSuccess || inventory.CanonicalSerial(d.SerialNumber) != a.config.Serial {
		return d, fmt.Errorf("%w: designated ADE device response incomplete", errOperation)
	}
	return d, nil
}

// binding rechecks both independently credentialed observations before mutation.
func (a *adeAccess) binding(ctx context.Context, s *Session) error {
	if err := adeReferenceAccount(ctx, s, a.config); err != nil {
		return err
	}
	detail, err := a.client.Account(ctx, a.config.Account)
	if err != nil {
		return adeError(ctx, "account readback")
	}
	if detail.ServerUUID != a.config.ServerUUID {
		return fmt.Errorf("%w: ADE server binding changed", errOperation)
	}
	return nil
}

// selection confines a one-shot worker to the designated serial. Quiescence is
// an explicit operator prerequisite: the API has no desired-selection CAS.
func (a *adeAccess) selection(ctx context.Context, s *Session) (adeSelection, error) {
	var v adeSelection
	if err := adeRequest(ctx, s, "GET", adeBase(a.config)+"/targets", nil, &v); err != nil {
		return v, err
	}
	if v.Default != "" {
		return v, fmt.Errorf("%w: ADE acceptance requires an account without a default profile", ErrBlocked)
	}
	for serial := range v.Serials {
		if serial != a.config.Serial {
			return v, fmt.Errorf("%w: ADE acceptance requires no other serial profile choices", ErrBlocked)
		}
	}
	return v, nil
}

// profileState distinguishes a genuinely unassigned device from inconsistent
// or pending remote profile evidence.
func profileState(d dep.Device) (string, error) {
	switch d.ProfileStatus {
	case dep.ProfileStatusAssigned, dep.ProfileStatusPushed:
		if d.ProfileUUID != "" {
			return d.ProfileUUID, nil
		}
	case dep.ProfileStatusEmpty, dep.ProfileStatusRemoved:
		if d.ProfileUUID == "" {
			return "", nil
		}
	}
	return "", fmt.Errorf("%w: ADE profile readback is incomplete", errOperation)
}
