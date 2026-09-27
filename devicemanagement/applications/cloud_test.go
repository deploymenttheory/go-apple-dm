package applications_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	applicationaws "github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/aws"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/azure"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/gcp"
)

// cloudFixture implements only the provider HTTP operations used in these contract
// tests. Clients remain the actual official SDKs, including request serialization.
type cloudFixture struct {
	t                  *testing.T
	mu                 sync.Mutex
	provider, endpoint string
	data               []byte
	blocks             map[string][]byte
	calls              []string
}

// ServeHTTP records requests and emulates provider upload, download and delete responses.
func (f *cloudFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	if r.URL.Query().Get("uploadType") == "resumable" {
		w.Header().Set("Location", f.endpoint+"/resumable")
		w.WriteHeader(http.StatusOK)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Error(err)
			http.Error(w, "read", 500)
			return
		}
		if r.URL.Query().Get("uploadType") == "multipart" {
			_, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if e != nil {
				f.t.Error(e)
				return
			}
			mr := multipart.NewReader(bytes.NewReader(data), params["boundary"])
			first, e := mr.NextPart()
			if e != nil {
				f.t.Error(e)
				return
			}
			if _, e = io.Copy(io.Discard, first); e != nil {
				f.t.Error(e)
				return
			}
			next, e := mr.NextPart()
			if e != nil {
				f.t.Error(e)
				return
			}
			data, e = io.ReadAll(next)
			if e != nil {
				f.t.Error(e)
				return
			}
		}
		if f.provider == "azure" && r.URL.Query().Get("comp") == "block" {
			f.blocks[r.URL.Query().Get("blockid")] = data
			w.WriteHeader(http.StatusCreated)
			return
		}
		if f.provider == "azure" && r.URL.Query().Get("comp") == "blocklist" {
			var list struct {
				Latest []string `xml:"Latest"`
			}
			// #nosec G709 -- Local SDK contract fixture; the authored client supplies only block IDs, and encoding/xml does not resolve external entities.
			if err = xml.Unmarshal(data, &list); err != nil {
				f.t.Error(err)
				return
			}
			data = nil
			for _, id := range list.Latest {
				data = append(data, f.blocks[id]...)
			}
		}
		if f.data != nil {
			if f.provider == "azure" {
				w.Header().Set("x-ms-error-code", "ConditionNotMet")
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>exists</Message></Error>`)
			return
		}
		if f.provider != "gcp" && r.Header.Get("If-None-Match") != "*" {
			f.t.Error("missing create-only condition")
		}
		f.data = bytes.Clone(data)
		w.Header().Set("ETag", `"test-etag"`)
		if f.provider == "gcp" {
			w.Header().Set("Content-Type", "application/json")
			if err = json.NewEncoder(w).Encode(map[string]any{"bucket": "bucket", "name": "prefix/id/package.pkg", "generation": "1", "size": strconv.Itoa(len(data))}); err != nil {
				f.t.Error(err)
			}
		} else {
			w.WriteHeader(http.StatusCreated)
		}
	case http.MethodGet:
		if f.data == nil {
			if f.provider == "azure" {
				w.Header().Set("x-ms-error-code", "BlobNotFound")
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		start, end := 0, len(f.data)-1
		raw := r.Header.Get("Range")
		if raw == "" {
			raw = r.Header.Get("x-ms-range")
		}
		if raw != "" {
			parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
			var err error
			start, err = strconv.Atoi(parts[0])
			if err != nil {
				f.t.Error(err)
				return
			}
			if len(parts) > 1 && parts[1] != "" {
				end, err = strconv.Atoi(parts[1])
				if err != nil {
					f.t.Error(err)
					return
				}
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(f.data)))
			w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
		}
		_, _ = w.Write(f.data[start : end+1])
	case http.MethodDelete:
		if f.data == nil {
			if f.provider == "azure" {
				w.Header().Set("x-ms-error-code", "BlobNotFound")
			}
			w.WriteHeader(http.StatusNotFound)
			if f.provider == "gcp" {
				_, _ = io.WriteString(w, `{"error":{"code":404,"message":"not found"}}`)
			} else {
				_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
			}
			return
		}
		f.data = nil
		if f.provider == "azure" {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		http.Error(w, "unexpected operation", 500)
	}
}

// TestCloudSDKRoundTrips proves each official SDK sends real uploads and range
// requests, preserves bytes, rejects replacement and deletes through its transport.
func TestCloudSDKRoundTrips(t *testing.T) {
	for _, provider := range []string{"aws", "azure", "gcp"} {
		t.Run(provider, func(t *testing.T) {
			f := &cloudFixture{t: t, provider: provider, blocks: map[string][]byte{}}
			endpoint := httptest.NewServer(f)
			defer endpoint.Close()
			f.endpoint = endpoint.URL
			var store applications.BlobStore
			var err error
			switch provider {
			case "aws":
				client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: &endpoint.URL, UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, RequestChecksumCalculation: awssdk.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: awssdk.ResponseChecksumValidationWhenRequired})
				store, err = applicationaws.New(client, "bucket", "prefix")
				if _, e := applicationaws.New(nil, "bucket", ""); e == nil {
					t.Fatal("nil client")
				}
				if _, e := applicationaws.New(client, "bucket", "../escape"); e == nil {
					t.Fatal("invalid prefix")
				}
			case "azure":
				client, e := azblob.NewClientWithNoCredential(endpoint.URL, &azblob.ClientOptions{ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}}})
				if e != nil {
					t.Fatal(e)
				}
				store, err = azure.New(client, "container", "prefix")
				if _, e := azure.New(nil, "container", ""); e == nil {
					t.Fatal("nil client")
				}
				if _, e := azure.New(client, "container", "../escape"); e == nil {
					t.Fatal("invalid prefix")
				}
			case "gcp":
				client, e := storage.NewClient(t.Context(), option.WithEndpoint(endpoint.URL), option.WithoutAuthentication(), option.WithHTTPClient(endpoint.Client()))
				if e != nil {
					t.Fatal(e)
				}
				defer func() {
					if e := client.Close(); e != nil {
						t.Error(e)
					}
				}()
				store, err = gcp.New(client, "bucket", "prefix")
				if _, e := gcp.New(nil, "bucket", ""); e == nil {
					t.Fatal("nil client")
				}
				if _, e := gcp.New(client, "bucket", "../escape"); e == nil {
					t.Fatal("invalid prefix")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("verified package bytes for SDK transfer")
			if err = store.Put(t.Context(), "id/package.pkg", bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatal(err)
			}
			if err = store.Put(t.Context(), "id/package.pkg", bytes.NewReader(data), int64(len(data))); err == nil {
				t.Fatal("replacement accepted")
			}
			for _, span := range [][2]int64{{0, -1}, {3, 9}, {3, -1}, {0, 0}} {
				body, e := store.Open(t.Context(), "id/package.pkg", span[0], span[1])
				if e != nil {
					t.Fatal(e)
				}
				got, e := io.ReadAll(body)
				closeErr := body.Close()
				if e != nil || closeErr != nil {
					t.Fatal(e, closeErr)
				}
				end := int64(len(data))
				if span[1] >= 0 {
					end = span[0] + span[1]
				}
				if !bytes.Equal(got, data[span[0]:end]) {
					t.Fatalf("range %v = %q", span, got)
				}
			}
			if err = store.Put(t.Context(), "../escape", bytes.NewReader(data), int64(len(data))); err == nil {
				t.Fatal("traversal accepted")
			}
			if err = store.Put(t.Context(), "bad-size.pkg", bytes.NewReader(data), 999); err == nil {
				t.Fatal("wrong size accepted")
			}
			if err = store.Delete(t.Context(), "id/package.pkg"); err != nil {
				t.Fatal(err)
			}
			if err = store.Delete(t.Context(), "id/package.pkg"); err != nil {
				t.Fatal("idempotent deletion", err)
			}
			if _, e := store.Open(t.Context(), "id/package.pkg", 0, -1); !errors.Is(e, applications.ErrNotFound) {
				t.Fatalf("missing object: %v", e)
			}
			for _, span := range [][2]int64{{-1, 1}, {0, -2}} {
				if _, e := store.Open(t.Context(), "id/package.pkg", span[0], span[1]); !errors.Is(e, applications.ErrInvalid) {
					t.Fatal(e)
				}
			}
			if _, e := store.Open(t.Context(), "../escape", 0, -1); !errors.Is(e, applications.ErrInvalid) {
				t.Fatal(e)
			}
			if e := store.Delete(t.Context(), "../escape"); !errors.Is(e, applications.ErrInvalid) {
				t.Fatal(e)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, e := store.Open(canceled, "id/package.pkg", 0, -1); !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			f.mu.Lock()
			calls := append([]string(nil), f.calls...)
			f.mu.Unlock()
			if provider == "gcp" {
				for _, input := range []io.ReadSeeker{brokenSource{Reader: bytes.NewReader([]byte("x")), readError: true}, brokenSource{Reader: bytes.NewReader([]byte("x"))}} {
					if e := store.Put(t.Context(), "broken.pkg", input, 10); e == nil {
						t.Fatal("short or failed source accepted")
					}
				}
			}
			if len(calls) < 6 {
				t.Fatalf("SDK did not reach transport: %v", calls)
			}
			t.Logf("verified %s SDK operations: %v", provider, calls)
		})
	}
}

// brokenSource models an input truncated or failed after its initial length check.
type brokenSource struct {
	*bytes.Reader
	readError bool
}

// Seek reports the pre-upload length for the source.
func (s brokenSource) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 10, nil
	}
	return s.Reader.Seek(offset, whence)
}

// Read fails or returns fewer bytes than the length previously reported.
func (s brokenSource) Read(p []byte) (int, error) {
	if s.readError {
		return 0, io.ErrUnexpectedEOF
	}
	return s.Reader.Read(p)
}
