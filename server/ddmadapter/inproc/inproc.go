package inproc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
)

// ContentTypeJSON is the content type of every DDM response body.
const ContentTypeJSON = "application/json"

// Backend serves one DeclarativeManagement check-in. *ddm.Engine
// satisfies it; tests stub it.
type Backend interface {
	Handle(ctx context.Context, id mdm.EnrollmentID, endpoint string, data []byte) (ddm.Response, error)
}

var _ Backend = (*ddm.Engine)(nil)

// ErrNoBackend is returned by the handler when Handler was given nil.
var ErrNoBackend = errors.New("inproc: backend is required")

// Handler returns a service.DMHandler that dispatches to b. Apple's
// answers are relayed as they are: 200 with a JSON body, the empty 200
// for status, 404 for a declaration outside the manifest. A malformed
// endpoint or status report is CodeBadRequest; anything else the backend
// reports is CodeInternal.
func Handler(b Backend) service.DMHandler {
	return func(ctx context.Context, _ *mdm.Request, ck *mdm.Checkin, m *checkin.DeclarativeManagement) (service.DMResponse, error) {
		if b == nil {
			return service.DMResponse{}, &service.Error{Code: service.CodeInternal, Err: ErrNoBackend}
		}
		if ck == nil || m == nil {
			return service.DMResponse{}, &service.Error{Code: service.CodeBadRequest, Err: fmt.Errorf("%w: nil DeclarativeManagement", service.ErrInvalidMessage)}
		}
		resp, err := b.Handle(ctx, ck.ID, m.Endpoint, m.Data)
		if err != nil {
			return service.DMResponse{}, &service.Error{Code: CodeFor(err), Err: err}
		}
		return Response(resp), nil
	}
}

// CodeFor maps a backend error to a service code: ErrBadEndpoint,
// ErrStatusTooLarge, and ErrStatusMalformed are the device's fault
// (CodeBadRequest); everything else is CodeInternal.
func CodeFor(err error) service.Code {
	if errors.Is(err, ddm.ErrBadEndpoint) || errors.Is(err, ddm.ErrStatusTooLarge) || errors.Is(err, ddm.ErrStatusMalformed) {
		return service.CodeBadRequest
	}
	return service.CodeInternal
}

// Response converts a backend response to the service shape. A zero
// status means 200.
func Response(r ddm.Response) service.DMResponse {
	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	return service.DMResponse{Body: r.Body, ContentType: ContentTypeJSON, Status: status}
}
