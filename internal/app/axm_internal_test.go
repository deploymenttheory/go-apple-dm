package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/axm"
)

func TestAxMStatus(t *testing.T) {
	cases := map[error]int{
		&axm.Error{Status: http.StatusNotFound}:        http.StatusNotFound,
		&axm.Error{Status: http.StatusConflict}:        http.StatusConflict,
		&axm.Error{Status: http.StatusTooManyRequests}: http.StatusTooManyRequests,
		fmt.Errorf("x: %w", axm.ErrArgument):           http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrActivityRule):       http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrLimit):              http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrWaitTimeout):        http.StatusGatewayTimeout,
		errors.New("transport"):                        http.StatusBadGateway,
	}
	for err, want := range cases {
		if got := axmStatus(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}

func TestListOptions(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=abc&cursor=c1", nil)
	if o := listOptions(r); o.Limit != 0 || o.Cursor != "c1" {
		t.Fatalf("%+v", o)
	}
	r = httptest.NewRequest(http.MethodGet, "/x?limit=7", nil)
	if o := listOptions(r); o.Limit != 7 {
		t.Fatalf("%+v", o)
	}
}
