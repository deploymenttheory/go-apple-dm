package lab

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

// liveArea is one live acceptance theme's private workspace subdirectory and
// the name used in its public errors. Areas share bounded private input,
// exclusive evidence scopes and reference API error handling; each area keeps
// its own classification of rejected administration requests.
type liveArea struct {
	dir   string
	label string
}

var (
	adeArea  = liveArea{dir: "ade", label: "ADE"}
	appsArea = liveArea{dir: "appsbooks", label: "Apps and Books"}
)

// liveInputLimit bounds private operator inputs such as acceptance scope and tokens.
const liveInputLimit = 1 << 20

// read returns a fixed-name private input of at most liveInputLimit bytes.
// A missing file is the unmet prerequisite named by missing. Errors never
// include the credential path or content.
func (l liveArea) read(s *Session, name, missing string) ([]byte, error) {
	if s.Env == nil || s.Env.Workspace == nil {
		return nil, fmt.Errorf("%w: private %s workspace required", ErrBlocked, l.label)
	}
	f, err := os.Open(s.Env.Workspace.path(l.dir, name)) // #nosec G304 -- Callers supply fixed filenames beneath the operator-owned private workspace.
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, missing)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: private %s input unavailable", errOperation, l.label)
	}
	b, err := io.ReadAll(io.LimitReader(f, liveInputLimit+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(b) > liveInputLimit {
		return nil, fmt.Errorf("%w: private %s input unreadable or oversized", errOperation, l.label)
	}
	return b, nil
}

// fail reports a failed operation without private upstream detail. The
// context error remains observable after cancellation or deadline expiry.
func (l liveArea) fail(ctx context.Context, operation string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %s %s interrupted", ctx.Err(), l.label, operation)
	}
	return fmt.Errorf("%w: %s %s failed", errOperation, l.label, operation)
}

// request calls the ordinary authenticated reference API and decodes a
// successful response into out. rejected classifies an HTTP error status; a
// nil result reports a generic failure of the named exchange. Errors omit
// response bodies, paths and designated identifiers.
func (l liveArea) request(ctx context.Context, s *Session, exchange string, rejected func(status int) error, method, path string, in, out any) error {
	var data []byte
	var err error
	if in != nil {
		data, err = jsonv2.Marshal(in)
		if err != nil {
			return l.fail(ctx, "request encoding")
		}
	}
	b, status, err := HTTP(ctx, s.Env.Client, s.Env.URL, s.Env.Token, method, path, bytes.NewReader(data))
	if err != nil {
		if classified := rejected(status); classified != nil {
			return classified
		}
		return l.fail(ctx, exchange+" request")
	}
	if out != nil && jsonv2.Unmarshal(b, out) != nil {
		return l.fail(ctx, exchange+" response")
	}
	return nil
}

// scope creates an owner-only evidence directory named prefix-<random> beneath
// the session's evidence directory, so every invocation, including a rerun,
// writes beneath its own exclusive directory. Missing parents are created with
// mode 0700; an existing parent retains its access policy.
func (l liveArea) scope(s *Session, prefix string) (*Session, string, error) {
	if s.Evidence == "" {
		return nil, "", fmt.Errorf("%w: private %s evidence directory required", ErrBlocked, l.label)
	}
	parent, err := filepath.Abs(s.Evidence)
	if err != nil {
		return nil, "", l.fail(context.Background(), "evidence path")
	}
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return nil, "", l.fail(context.Background(), "evidence directory")
	}
	id := randomID()
	copy := *s
	copy.Evidence = filepath.Join(parent, prefix+"-"+id)
	if err = privatefile.Mkdir(copy.Evidence); err != nil {
		return nil, "", l.fail(context.Background(), "exclusive evidence directory")
	}
	return &copy, id, nil
}
