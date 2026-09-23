package dmctl

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/lab"
)

// TestReplacementOutputRetainsAttemptWhenWakeFails checks that replacement output retains attempt
// when wake fails.
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
	instance := &lab.Environment{Instance: lab.Instance{URL: srv.URL}, Client: srv.Client()}
	var output bytes.Buffer
	err := labReplace(t.Context(), &env{stdout: &output}, instance, "device", "scep")
	if err == nil || !strings.Contains(output.String(), "approved-attempt") {
		t.Fatal("lost attempt or masked wake failure", output.String(), err)
	}
}

type replacementOutputFailure struct{}

// Write returns io.ErrClosedPipe to simulate an output failure.
func (replacementOutputFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestReplacementOutputFailureIsReported checks replacement output failure is reported.
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
	instance := &lab.Environment{Instance: lab.Instance{URL: srv.URL}, Client: srv.Client()}
	if err := labReplace(
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
