package appsbooks_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// TestAssetRepresentations exercises the actual client against independent wire fixtures.
func TestAssetRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		filter         *bool
		kinds          []appsbooks.StockKind
	}{
		{"finite", `"assets":[{"adamId":"1","availableCount":0,"assignedCount":2,"retiredCount":0,"totalCount":2}]`, new(false), []appsbooks.StockKind{appsbooks.StockFinite}},
		{"missing", `"assets":[{"adamId":"2","availableCount":null}]`, nil, []appsbooks.StockKind{appsbooks.StockUnknown}},
		{"unlimited", `"unlimitedAssets":[{"adamId":"3","assignedCount":4,"productType":"Book","deviceAssignable":true,"revocable":true,"future":{"large":9007199254740993,"null":null,"false":false}}]`, new(true), []appsbooks.StockKind{appsbooks.StockUnlimited}},
		{"sentinel", `"assets":[{"adamId":"4","totalCount":2147483647,"availableCount":2147483640}]`, nil, []appsbooks.StockKind{appsbooks.StockUnlimited}},
		{"mixed", `"assets":[{"availableCount":5,"totalCount":10},{"totalCount":2147483647}],"unlimitedAssets":[{"assignedCount":0}]`, new(true), []appsbooks.StockKind{appsbooks.StockFinite, appsbooks.StockUnlimited, appsbooks.StockUnlimited}},
		{"empty", `"assets":[],"unlimitedAssets":[]`, new(true), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `{"uId":"L","currentPageIndex":0,"versionId":"first","futurePage":{"null":null,"large":9223372036854775807},` + tc.response + `}`
			f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				v, ok := q["unlimited"]
				if tc.filter == nil {
					if ok {
						t.Error("omitted filter sent", v)
					}
				} else if !ok || q.Get("unlimited") != fmt.Sprint(*tc.filter) {
					t.Error("filter", q)
				}
				_, _ = fmt.Fprint(w, wire)
			})
			page, err := f.client.Assets(t.Context(), appsbooks.AssetsQuery{Unlimited: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			if string(page.Raw) != wire {
				t.Fatalf("page source changed: %s", page.Raw)
			}
			for _, a := range page.Assets {
				if len(a.Raw) == 0 {
					t.Fatal("missing asset source")
				}
			}
			if tc.name == "missing" && page.Assets[0].AvailableCount != nil {
				t.Fatal("missing became a numeric zero")
			}
			if tc.name == "finite" && (page.Assets[0].AvailableCount == nil || *page.Assets[0].AvailableCount != 0) {
				t.Fatal("explicit zero lost")
			}
			if tc.name == "sentinel" && *page.Assets[0].TotalCount != 2147483647 {
				t.Fatal("sentinel source changed")
			}
			if tc.name == "unlimited" {
				a := page.UnlimitedAssets[0]
				if !strings.Contains(string(a.Raw), `9007199254740993`) {
					t.Fatal("unknown large integer lost")
				}
				if !errors.Is(a.CheckAssignment(true, false), appsbooks.ErrInput) || !errors.Is(a.CheckAssignment(false, true), appsbooks.ErrInput) {
					t.Fatal("book restrictions lost")
				}
			}
			var kinds []appsbooks.StockKind
			version, err := f.client.WalkAssets(t.Context(), appsbooks.AssetsQuery{Unlimited: tc.filter}, 1, func(a appsbooks.AssetInventory) error {
				if (a.Record == nil) == (a.Unlimited == nil) {
					t.Fatal("ambiguous representation")
				}
				kinds = append(kinds, a.Stock().Kind)
				return nil
			})
			if err != nil || version != "first" || !reflect.DeepEqual(kinds, tc.kinds) {
				t.Fatalf("walk %s %v %v", version, kinds, err)
			}
		})
	}
}

// TestAssetTraversalFailures verifies mixed and empty pages, caps, cancellation and failures.
func TestAssetTraversalFailures(t *testing.T) {
	for _, mode := range []string{"complete", "cap", "callback", "cancel", "cancel-final", "http", "malformed", "regression"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("unlimited") != "true" || r.URL.Query().Get("adamId") != "123" {
					t.Error("filters lost")
				}
				switch r.URL.Query().Get("pageIndex") {
				case "0":
					_, _ = fmt.Fprint(w, `{"uId":"L","currentPageIndex":0,"nextPageIndex":2,"versionId":"first","assets":[]}`)
				case "2":
					switch mode {
					case "http":
						w.WriteHeader(http.StatusBadRequest)
						_, _ = fmt.Fprint(w, `{"errorNumber":9600}`)
					case "malformed":
						_, _ = fmt.Fprint(w, `{"assets":[{"totalCount":"bad"}]}`)
					case "regression":
						_, _ = fmt.Fprint(w, `{"uId":"L","currentPageIndex":2,"nextPageIndex":2}`)
					case "cancel-final":
						_, _ = fmt.Fprint(w, `{"uId":"L","currentPageIndex":2,"unlimitedAssets":[{}]}`)
					default:
						_, _ = fmt.Fprint(w, `{"uId":"L","currentPageIndex":2,"versionId":"later","assets":[{"availableCount":2}],"unlimitedAssets":[{}]}`)
					}
				default:
					t.Error("unexpected page", r.URL.Query())
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cap := 2
			if mode == "cap" {
				cap = 1
			}
			visited := 0
			stop := errors.New("stop")
			version, err := f.client.WalkAssets(ctx, appsbooks.AssetsQuery{Unlimited: new(true), AdamID: "123", PageIndex: 99}, cap, func(a appsbooks.AssetInventory) error {
				visited++
				if mode == "callback" {
					return stop
				}
				if mode == "cancel" || mode == "cancel-final" {
					cancel()
				}
				return nil
			})
			switch mode {
			case "complete":
				if err != nil || version != "first" || visited != 2 {
					t.Fatalf("%s %d %v", version, visited, err)
				}
			case "cap":
				if !errors.Is(err, appsbooks.ErrLimit) || calls != 1 {
					t.Fatal(calls, err)
				}
			case "callback":
				if !errors.Is(err, stop) || visited != 1 {
					t.Fatal(visited, err)
				}
			case "cancel", "cancel-final":
				if !errors.Is(err, context.Canceled) || visited != 1 {
					t.Fatal(visited, err)
				}
			default:
				if err == nil {
					t.Fatal("expected failure")
				}
			}
			if mode != "complete" && version != "" {
				t.Fatal("failed walk returned checkpoint")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("request after cancellation") })
	if _, err := f.client.WalkAssets(ctx, appsbooks.AssetsQuery{}, 1, func(appsbooks.AssetInventory) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.client.WalkAssets(t.Context(), appsbooks.AssetsQuery{}, 1, nil); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
}

// TestAssetDecodeFailures also covers direct public decoding and empty stock values.
func TestAssetDecodeFailures(t *testing.T) {
	for _, value := range []any{new(appsbooks.AssetRecord), new(appsbooks.UnlimitedAssetRecord), new(appsbooks.AssetsPage)} {
		if err := json.Unmarshal([]byte(`{"assignedCount":"invalid","assets":false}`), value); err == nil {
			t.Fatalf("accepted invalid %T", value)
		}
	}
	if got := (appsbooks.AssetInventory{}).Stock(); got.Kind != appsbooks.StockUnknown {
		t.Fatal(got)
	}
	a := appsbooks.UnlimitedAssetRecord{ProductType: "App", DeviceAssignable: true, Revocable: true}
	if err := a.CheckAssignment(true, true); err != nil {
		t.Fatal(err)
	}
}
