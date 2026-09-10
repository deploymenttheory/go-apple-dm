package dmctl

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
)

func TestReplacementOutputRetainsAttemptWhenWakeFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/replacement") {
			_, _ = io.WriteString(w, `{"ID":"approved-attempt"}`)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	instance := &bench.Environment{Instance: bench.Instance{URL: srv.URL}, Client: srv.Client()}
	var output bytes.Buffer
	err := benchReplace(t.Context(), &env{stdout: &output}, instance, "device", "scep")
	if err == nil || !strings.Contains(output.String(), "approved-attempt") {
		t.Fatal("lost attempt or masked wake failure", output.String(), err)
	}
}

type replacementOutputFailure struct{}

func (replacementOutputFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestReplacementOutputFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/replacement") {
			_, _ = io.WriteString(w, `{"ID":"attempt"}`)
		} else {
			_, _ = io.WriteString(w, `{"Sent":true}`)
		}
	}))
	defer srv.Close()
	instance := &bench.Environment{Instance: bench.Instance{URL: srv.URL}, Client: srv.Client()}
	if err := benchReplace(
		t.Context(),
		&env{stdout: replacementOutputFailure{}},
		instance,
		"device",
		"scep",
	); !errors.Is(
		err,
		io.ErrClosedPipe,
	) {
		t.Fatal(err)
	}
}
