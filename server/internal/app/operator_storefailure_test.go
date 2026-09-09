package app

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/apppush"
	"github.com/deploymenttheory/go-apple-dm/state"
)

type unavailableAppState struct {
	state.Store
	failure error
}

func (s unavailableAppState) List(context.Context, string, string, int) ([]state.Record, error) {
	return nil, s.failure
}

func TestAppCredentialListingReportsStoreFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		failure error
		status  int
	}{{state.ErrNotFound, 404}, {state.ErrInvalid, 400}, {io.ErrUnexpectedEOF, 500}} {
		a := &App{appPushStore: &apppush.Store{State: unavailableAppState{failure: tc.failure}}}
		req := httptest.NewRequest("GET", "/apppush/credentials", nil)
		w := httptest.NewRecorder()
		a.listAppPush(w, req)
		if w.Code != tc.status {
			t.Fatalf("%v: HTTP %d", tc.failure, w.Code)
		}
		if errors.Is(tc.failure, io.ErrUnexpectedEOF) && w.Body.String() == tc.failure.Error() {
			t.Fatal("backend detail leaked")
		}
	}
}
