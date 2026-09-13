package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

var errAdminResponse = errors.New("app: local administrative operation failed")

func (a *App) localAdmin(w http.ResponseWriter, r *http.Request, p adminauth.Principal, rt adminRoute) {
	buffer := &adminResponse{header: make(http.Header)}
	err := a.eventPublisher.Run(r.Context(), func(ctx context.Context) error {
		inside := r.WithContext(ctx)
		rt.Handler.ServeHTTP(buffer, inside)
		if buffer.err != nil {
			return buffer.err
		}
		if buffer.status >= 300 {
			return errAdminResponse
		}
		return a.auditAction(inside, p, rt)
	})
	if errors.Is(err, errAdminResponse) && !errors.Is(err, event.ErrCapture) {
		// The allowed request is still an occurrence when validation failed.
		// Record it after rollback; none of its local writes can survive.
		err = a.auditAction(r, p, rt)
	}
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, event.ErrCapture)
		return
	}
	for key, values := range buffer.header {
		w.Header()[key] = values
	}
	if buffer.status != 0 {
		w.WriteHeader(buffer.status)
	}
	// The handler already encoded this body and supplied its content type.
	_, _ = w.Write(buffer.body.Bytes()) // #nosec G705 -- bounded response encoded by an existing admin handler
	a.kickNotifier(rt, r, buffer.status)
}

// adminResponse prevents a token or a success response escaping before commit.
// Local mutation responses are small; refuse unexpectedly large output.
type adminResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
	err    error
}

func (w *adminResponse) Header() http.Header { return w.header }
func (w *adminResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *adminResponse) Write(b []byte) (int, error) {
	if w.body.Len()+len(b) > MaxAdminBody {
		w.err = ErrBodyTooLarge
		return 0, w.err
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(b)
}
