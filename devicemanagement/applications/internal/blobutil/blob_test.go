package blobutil_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/internal/blobutil"
)

// failingSeeker models a source whose length cannot be determined.
type failingSeeker struct{ io.Reader }

// Seek returns an I/O failure without consuming source bytes.
func (failingSeeker) Seek(int64, int) (int64, error) { return 0, io.ErrUnexpectedEOF }

// TestInputBoundaries checks cancellation, malformed keys and unreadable sources.
func TestInputBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (blobutil.Reader{Context: ctx, Input: strings.NewReader("x")}).Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := blobutil.Input(t.Context(), nil, 1); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
	if err := blobutil.Input(t.Context(), failingSeeker{Reader: strings.NewReader("x")}, 1); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if _, err := blobutil.Object("../escape", "key"); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
}
