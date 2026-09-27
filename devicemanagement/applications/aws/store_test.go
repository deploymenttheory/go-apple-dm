package aws_test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	sdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	applicationaws "github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/aws"
)

// TestMultipartCommitAndAbort exercises the SDK multipart wire exchange and
// verifies that failed part uploads trigger an abort rather than publication.
func TestMultipartCommitAndAbort(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			var mu sync.Mutex
			parts := map[int][]byte{}
			committed, aborted := false, false
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>fixture-upload</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == http.MethodPut:
					n, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
					if err != nil {
						t.Error(err)
						return
					}
					if fail && n == 2 {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
						return
					}
					data, err := io.ReadAll(io.LimitReader(r.Body, 17<<20))
					if err != nil {
						t.Error(err)
						return
					}
					parts[n] = data
					w.Header().Set("ETag", `"part-`+strconv.Itoa(n)+`"`)
				case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "fixture-upload":
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("multipart commit can overwrite")
					}
					var completion struct {
						Parts []struct {
							Number int `xml:"PartNumber"`
						} `xml:"Part"`
					}
					if err := xml.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&completion); err != nil {
						t.Error(err)
						return
					}
					if len(completion.Parts) != 2 || completion.Parts[0].Number != 1 || completion.Parts[1].Number != 2 {
						t.Errorf("invalid completion sequence: %+v", completion)
					}
					committed = true
					_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>id/package.pkg</Key><ETag>complete</ETag></CompleteMultipartUploadResult>`)
				case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "fixture-upload":
					aborted = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected SDK request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer endpoint.Close()
			client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: &endpoint.URL, UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, RequestChecksumCalculation: sdk.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: sdk.ResponseChecksumValidationWhenRequired})
			store, err := applicationaws.New(client, "bucket", "")
			if err != nil {
				t.Fatal(err)
			}
			data := bytes.Repeat([]byte("p"), (16<<20)+1)
			err = store.Put(t.Context(), "id/package.pkg", bytes.NewReader(data), int64(len(data)))
			mu.Lock()
			defer mu.Unlock()
			if fail {
				if err == nil || committed || !aborted {
					t.Fatalf("failure: committed=%v aborted=%v err=%v", committed, aborted, err)
				}
			} else {
				if err != nil || !committed || aborted {
					t.Fatalf("success: committed=%v aborted=%v err=%v", committed, aborted, err)
				}
				actual := append(parts[1], parts[2]...)
				if !bytes.Equal(actual, data) {
					t.Fatal("multipart bytes differ")
				}
			}
		})
	}
}

// sparseSource reports a large source but ends early when its bytes are consumed.
type sparseSource struct {
	*bytes.Reader
	size int64
}

// Seek reports the original size before a source truncation.
func (s sparseSource) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return s.size, nil
	}
	return s.Reader.Seek(offset, whence)
}

// TestMultipartProtocolFailures checks missing provider fields, failed creation,
// source truncation, failed aborts and sizes beyond the supported multipart limit.
func TestMultipartProtocolFailures(t *testing.T) {
	for _, failure := range []string{"create", "upload-id", "etag", "read", "size"} {
		t.Run(failure, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
					if failure == "size" {
						t.Error("oversized source contacted S3")
					}
					if failure == "create" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
						return
					}
					if failure == "upload-id" {
						_, _ = io.WriteString(w, `<InitiateMultipartUploadResult/>`)
						return
					}
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>test</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == http.MethodPut:
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
					}
					// Intentionally omit the part ETag, which is required for completion.
				case r.Method == http.MethodDelete:
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
				default:
					t.Error("unexpected request", r.Method)
				}
			}))
			defer endpoint.Close()
			client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: &endpoint.URL, UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, RequestChecksumCalculation: sdk.RequestChecksumCalculationWhenRequired})
			store, err := applicationaws.New(client, "bucket", "")
			if err != nil {
				t.Fatal(err)
			}
			size := int64((16 << 20) + 1)
			var input io.ReadSeeker = bytes.NewReader(bytes.Repeat([]byte("x"), int(size)))
			if failure == "read" || failure == "size" {
				if failure == "size" {
					size = (16<<20)*10000 + 1
				}
				input = sparseSource{Reader: bytes.NewReader(nil), size: size}
			}
			err = store.Put(t.Context(), "test.pkg", input, size)
			if err == nil {
				t.Fatal("invalid multipart exchange succeeded")
			}
			if (failure == "etag" || failure == "read") && !strings.Contains(err.Error(), "abort S3 multipart upload") {
				t.Fatal("lost abort failure", err)
			}
		})
	}
}
