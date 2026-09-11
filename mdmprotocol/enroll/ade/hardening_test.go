package ade_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/ade"
)

func TestEnrollmentErrorsAreNotCacheable(t *testing.T) {
	w := httptest.NewRecorder()
	ade.New(ade.Config{}).
		ServeHTTP(w, httptest.NewRequest(http.MethodPost, "https://mdm.example/enroll", strings.NewReader("invalid")))
	if w.Code < 400 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable enrollment error", w.Code, w.Header())
	}
}
