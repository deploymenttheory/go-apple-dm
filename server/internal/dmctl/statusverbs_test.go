package dmctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollmentStatusPages(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write(
				[]byte(`{"Items":[{"Path":"test.a","Value":"dHJ1ZQ=="}],"NextCursor":"next"}`),
			)
		} else {
			_, _ = w.Write([]byte(`{"Items":[{"Path":"test.b"}],"NextCursor":""}`))
		}
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "token"
	out, _, err := run(
		t,
		env,
		"enrollments",
		"status",
		"values",
		"user",
		"U",
		"-parent",
		"D",
		"-prefix",
		"test.",
		"-all",
		"-limit",
		"1",
	)
	if err != nil || !strings.Contains(out, "test.b") || !strings.Contains(out, "true") ||
		len(calls) != 2 {
		t.Fatal(out, calls, err)
	}
	for _, call := range calls {
		if !strings.Contains(call, "parent=D") || !strings.Contains(call, "prefix=test.") {
			t.Fatal(call)
		}
	}
	for _, args := range [][]string{{"enrollments", "status"}, {"enrollments", "status", "bad"}, {"enrollments", "status", "errors", "device", "D", "-prefix", "x"}} {
		if _, _, err := run(t, env, args...); err == nil {
			t.Fatal(args)
		}
	}
}
