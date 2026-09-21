package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestStatusPages checks enrollment status pagination, redacted projections, channel isolation,
// and authorization.
func TestStatusPages(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cfg := app.Config{
				Storage:        backend,
				BootstrapToken: "secret",
				DSN:            filepath.Join(t.TempDir(), "status.db"),
			}
			a := build(t, cfg)
			srv := serve(t, a)
			id := enrollment("D")
			values := make([]ddm.StatusValue, 0, 1006)
			for i := range 1005 {
				values = append(values, ddm.StatusValue{Path: fmt.Sprintf("test.%04d", i), Value: []byte("true")})
			}
			values = append(
				values,
				ddm.StatusValue{Path: "mdm.push-token", Value: []byte(`"secret-token"`)},
			)
			err := a.Engine.Store().Update(t.Context(), func(tx ddm.Tx) error {
				_, err := tx.PutStatus(t.Context(), id, ddm.StatusUpdate{
					Values:      values,
					ReceivedAt:  time.Now(),
					KeepReports: 10,
					Raw: []byte(
						`{"StatusItems":{"mdm":{"push-token":"secret-token"}},"Errors":[]}`,
					),
					Errors: []ddm.StatusError{
						{
							StatusItem: "test",
							Reasons:    []byte(`[{"code":"invalid","description":"secret-token"}]`),
						},
					},
				})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			base := "/admin/v1/enrollments/device/D/status/"
			var first paging.Result[ddm.StatusValue]
			decode(t, do(t, srv, "GET", base+"values", "secret", nil), 200, &first)
			if len(first.Items) != 1000 || first.NextCursor == "" {
				t.Fatal("default page", len(first.Items), first.NextCursor)
			}
			seen := map[string]bool{}
			cursor := ""
			for {
				var page paging.Result[ddm.StatusValue]
				decode(
					t,
					do(
						t,
						srv,
						"GET",
						base+"values?limit=113&prefix=test.&cursor="+url.QueryEscape(cursor),
						"secret",
						nil,
					),
					200,
					&page,
				)
				for _, v := range page.Items {
					if seen[v.Path] {
						t.Fatal("duplicate", v.Path)
					}
					seen[v.Path] = true
				}
				cursor = page.NextCursor
				if cursor == "" {
					break
				}
			}
			if len(seen) != 1005 {
				t.Fatal("lost records", len(seen))
			}
			var reports paging.Result[ddm.StatusReportRecord]
			decode(t, do(t, srv, "GET", base+"reports", "secret", nil), 200, &reports)
			if len(reports.Items) != 1 ||
				strings.Contains(string(reports.Items[0].Raw), "secret-token") {
				t.Fatal("report projection", reports)
			}
			var errs paging.Result[ddm.StatusError]
			decode(t, do(t, srv, "GET", base+"errors", "secret", nil), 200, &errs)
			if len(errs.Items) != 1 ||
				strings.Contains(string(errs.Items[0].Reasons), "secret-token") {
				t.Fatal("error projection", errs)
			}
			stored, err := a.Engine.StatusReports(t.Context(), id, paging.Page{})
			if err != nil || !strings.Contains(string(stored.Items[0].Raw), "secret-token") {
				t.Fatal("projection modified store", err)
			}
			var empty paging.Result[ddm.StatusValue]
			seed(t, a, "P")
			// DDM IDs are globally unique: a different channel cannot read D's rows.
			decode(
				t,
				do(
					t,
					srv,
					"GET",
					"/admin/v1/enrollments/user/D/status/values?parent=P",
					"secret",
					nil,
				),
				404,
				&empty,
			)
			if len(empty.Items) != 0 {
				t.Fatal("cross-channel leak")
			}
			for _, kind := range []string{"values", "errors", "reports"} {
				for _, suffix := range []string{"?limit=bad", "?cursor=!!!"} {
					// Values use a lexical path cursor; any string is a valid bound.
					if kind == "values" && suffix == "?cursor=!!!" {
						continue
					}
					res := do(t, srv, "GET", base+kind+suffix, "secret", nil)
					if res.StatusCode != http.StatusBadRequest {
						t.Fatal(kind, suffix, res.StatusCode)
					}
				}
				if res := do(t, srv, "GET", base+kind, "wrong", nil); res.StatusCode != 401 {
					t.Fatal("unauthorized", res.StatusCode)
				}
			}
		})
	}
}
