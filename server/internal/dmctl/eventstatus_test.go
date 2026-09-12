package dmctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusReportsEventOverload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(
			[]byte(
				`{"Role":"all","EventDelivery":{"Async":true,"Workers":8,"QueueCapacity":1024,"Queued":10,"InFlight":8,"Accepted":20,"Delivered":2,"Failed":0,"TimedOut":0,"Rejected":99,"Abandoned":0,"DeliveryTimeout":"30s"}}`,
			),
		)
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "token"
	for _, mode := range []string{"human", "json"} {
		out, _, err := run(t, env, "-output", mode, "status")
		if err != nil {
			t.Fatal(err)
		}
		want := "rejected=99"
		if mode == "json" {
			want = `"Rejected":99`
		}
		if !strings.Contains(out, want) || !strings.Contains(out, "30s") {
			t.Fatal(out)
		}
	}
}
