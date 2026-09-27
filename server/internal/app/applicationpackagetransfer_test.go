package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// packageDeadlineWriter exposes deadline control without depending on wall-clock waits.
type packageDeadlineWriter struct {
	*httptest.ResponseRecorder
	read, write       time.Time
	readErr, writeErr error
}

// SetReadDeadline records the requested upload deadline and an injected failure.
func (w *packageDeadlineWriter) SetReadDeadline(d time.Time) error {
	w.read = d
	return w.readErr
}

// SetWriteDeadline records the response deadline and an injected failure.
func (w *packageDeadlineWriter) SetWriteDeadline(d time.Time) error {
	w.write = d
	return w.writeErr
}

// TestPackageTransferDeadlines checks audit-wrapper forwarding, bounded contexts,
// unsupported embedded writers and refusal to transfer after deadline failures.
func TestPackageTransferDeadlines(t *testing.T) {
	failure := errors.New("private deadline failure")
	for _, tc := range []struct {
		name              string
		readBody          bool
		readErr, writeErr error
		want              int
	}{
		{"upload", true, nil, nil, 204},
		{"download", false, nil, nil, 204},
		{"unsupported", true, http.ErrNotSupported, http.ErrNotSupported, 204},
		{"read failure", true, failure, nil, 503},
		{"write failure", true, nil, failure, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &packageDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), readErr: tc.readErr, writeErr: tc.writeErr}
			var transferContext context.Context
			handler := packageTransfer(http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
				transferContext = r.Context()
				deadline, ok := r.Context().Deadline()
				if !ok || !deadline.Equal(w.write) {
					t.Error("storage and connection deadlines differ")
				}
				response.WriteHeader(http.StatusNoContent)
			}), tc.readBody, time.Minute)
			before := time.Now()
			handler.ServeHTTP(&statusRecorder{ResponseWriter: w}, httptest.NewRequestWithContext(t.Context(), "POST", "/", nil))
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.want == 204 {
				if transferContext == nil || !errors.Is(transferContext.Err(), context.Canceled) {
					t.Fatal("transfer context was not released")
				}
				if w.write.Before(before.Add(time.Minute)) || w.write.After(time.Now().Add(time.Minute)) {
					t.Fatal("unbounded response deadline", w.write)
				}
				if tc.readBody && !w.read.Equal(w.write) || !tc.readBody && !w.read.IsZero() {
					t.Fatal("incorrect upload deadline", w.read)
				}
			} else if transferContext != nil {
				t.Fatal("transfer ran after deadline setup failed")
			}
		})
	}
}

// TestPackageTransferContextExpires bounds storage operations even when an embedder
// supplies a response writer without connection-deadline support.
func TestPackageTransferContextExpires(t *testing.T) {
	called := false
	handler := packageTransfer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		<-r.Context().Done()
		if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			t.Error(r.Context().Err())
		}
	}), true, -time.Second)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "POST", "/", nil))
	if !called {
		t.Fatal("embedded transfer was not called")
	}
}

// TestPackageTransferRenewsConnectionDeadlines proves streamed request and response
// bytes survive expired ordinary-request deadlines through the audit wrapper.
func TestPackageTransferRenewsConnectionDeadlines(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
			t.Error(err)
			return
		}
		if err := controller.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
			t.Error(err)
			return
		}
		packageTransfer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(ready)
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}), true, time.Minute).ServeHTTP(&statusRecorder{ResponseWriter: w}, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	input, output := io.Pipe()
	defer func() { _ = input.Close() }()
	written := make(chan error, 1)
	go func() {
		select {
		case <-ready:
			_, err := io.WriteString(output, "package bytes")
			written <- errors.Join(err, output.Close())
		case <-ctx.Done():
			written <- output.CloseWithError(ctx.Err())
		}
	}()
	request, err := http.NewRequestWithContext(ctx, "POST", srv.URL, input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != 200 || string(data) != "package bytes" || readErr != nil || closeErr != nil {
		t.Fatalf("transfer: status=%d data=%q read=%v close=%v", response.StatusCode, data, readErr, closeErr)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
