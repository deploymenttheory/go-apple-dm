package dmctl

import (
	"errors"
	"io"
	"testing"
	"time"
)

// TestLabModuleTimeoutIsIndependentOfRequestTimeout protects the two distinct
// deadlines and their flag precedence without a network dependency.
func TestLabModuleTimeoutIsIndependentOfRequestTimeout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		args            []string
		module, request time.Duration
		invalid         bool
	}{
		{"metadata default", nil, 0, 17 * time.Second, false},
		{"module override", []string{"-module-timeout", "35m"}, 35 * time.Minute, 17 * time.Second, false},
		{"request first", []string{"-timeout", "19s", "-module-timeout", "35m"}, 35 * time.Minute, 19 * time.Second, false},
		{"request last", []string{"-module-timeout", "35m", "-timeout", "19s"}, 35 * time.Minute, 19 * time.Second, false},
		{"last module wins", []string{"-module-timeout", "35m", "-module-timeout", "40m"}, 40 * time.Minute, 17 * time.Second, false},
		{"negative", []string{"-module-timeout", "-1s"}, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{stderr: io.Discard, opts: options{timeout: 17 * time.Second}}
			f := newLabFlags(e, "lab run")
			_, err := f.parse(tc.args)
			if tc.invalid {
				if !errors.Is(err, ErrUsage) {
					t.Fatal("negative deadline accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if *f.moduleTimeout != tc.module || e.opts.timeout != tc.request {
				t.Fatal("module and HTTP request deadlines crossed")
			}
		})
	}
}
