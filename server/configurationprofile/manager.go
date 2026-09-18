package configurationprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Config uses the application's encrypted state store. Engine supplies device
// eligibility and advertised declarations; BaseURL is the public HTTPS URL.
type Config struct {
	State   state.Store
	Engine  *ddm.Engine
	BaseURL string
	Target  func(context.Context, mdm.EnrollmentID) (support.Target, error)
}

type Manager struct{ cfg Config }

func New(cfg Config) (*Manager, error) {
	if cfg.State == nil || cfg.Engine == nil {
		return nil, ddm.ErrInvalid
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Manager{cfg: cfg}, nil
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func read[T any](ctx context.Context, reader state.Reader, key string) (T, error) {
	var out T
	r, err := reader.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return out, ddm.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(r.Value, &out); err != nil {
		return out, fmt.Errorf("configurationprofile: stored record: %w", err)
	}
	return out, nil
}

func put(ctx context.Context, tx state.Tx, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: b})
}

func list[T any](ctx context.Context, st state.Store, prefix string, page paging.Page) (paging.Result[T], error) {
	var out paging.Result[T]
	rows, err := st.List(ctx, prefix, prefix+page.Cursor, page.Size()+1)
	if err != nil {
		return out, err
	}
	for _, row := range rows[:min(len(rows), page.Size())] {
		var item T
		if err := json.Unmarshal(row.Value, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(rows) > page.Size() {
		out.NextCursor = strings.TrimPrefix(rows[page.Size()-1].Key, prefix)
	}
	return out, nil
}
