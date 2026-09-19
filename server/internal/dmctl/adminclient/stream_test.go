package adminclient_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// gatedBody cannot finish producing its bytes until the server has consumed
// its prefix. A client that buffers the entire upload before sending cannot
// complete this exchange.
type gatedBody struct {
	ctx     context.Context
	read    <-chan struct{}
	prefix  *strings.Reader
	pending bool
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if b.pending {
		select {
		case <-b.read:
			b.pending = false
			b.prefix = strings.NewReader("\x00\xffsuffix")
			return b.prefix.Read(p)
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	return 0, io.EOF
}

func TestBinaryUploadStreamsBeforeEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	read := make(chan struct{})
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := make([]byte, len("prefix"))
		_, err := io.ReadFull(r.Body, prefix)
		close(read)
		if err != nil || string(prefix) != "prefix" {
			t.Errorf("upload prefix: %q, %v", prefix, err)
		}
		suffix, err := io.ReadAll(r.Body)
		if err != nil || string(suffix) != "\x00\xffsuffix" || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("upload suffix: %q, %v", suffix, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	body := &gatedBody{ctx: ctx, read: read, prefix: strings.NewReader("prefix"), pending: true}
	if _, err := c.DoWithHeaders(ctx, http.MethodPost, "/authoring/app-identities/artifacts", nil, body, http.Header{"Content-Type": {"application/octet-stream"}}); err != nil {
		t.Fatal(err)
	}
}
