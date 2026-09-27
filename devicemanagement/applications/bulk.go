package applications

import "context"

// DeleteRequest pins a package deletion to the revision reviewed by the caller.
type DeleteRequest struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// DeleteResult preserves individual failures in a bulk operation. Error is suitable
// for an authenticated API response; Err retains classification for Go callers.
type DeleteResult struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
	Err   error  `json:"-"`
}

// DeleteMany validates the entire request before performing up to 1000 deletions
// in caller order. It is not atomic across packages or object stores. Put children
// before parents. A failure is reported per package and does not conceal successes.
func (m *Manager) DeleteMany(ctx context.Context, requests []DeleteRequest) ([]DeleteResult, error) {
	if len(requests) == 0 || len(requests) > 1000 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range requests {
		if !ValidID(r.ID) || r.Revision == "" || seen[r.ID] {
			return nil, ErrInvalid
		}
		seen[r.ID] = true
	}
	out := make([]DeleteResult, 0, len(requests))
	for _, r := range requests {
		err := m.Delete(ctx, r.ID, r.Revision)
		item := DeleteResult{ID: r.ID, Err: err}
		if err != nil {
			item.Error = err.Error()
		}
		out = append(out, item)
	}
	return out, nil
}
