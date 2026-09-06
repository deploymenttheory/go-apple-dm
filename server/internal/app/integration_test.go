//go:build integration

package app_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestBuildSQLRoles opens the all role on the CI databases so the
// PostgreSQL and MySQL storage paths (enrollment store plus the engine's
// migration set on the same database) are exercised.
func TestBuildSQLRoles(t *testing.T) {
	cases := map[string]string{
		"postgres": os.Getenv("TEST_POSTGRES_DSN"),
		"mysql":    os.Getenv("TEST_MYSQL_DSN"),
	}
	for backend, dsn := range cases {
		t.Run(backend, func(t *testing.T) {
			if dsn == "" {
				t.Skipf("TEST_%s_DSN not set (make testdb-up prints it)", backend)
			}
			a := build(t, app.Config{Role: app.RoleAll, Storage: backend, DSN: dsn, AdminToken: "t"})
			srv := serve(t, a)
			if got := get(t, srv.URL+"/healthz", ""); got != http.StatusOK {
				t.Fatalf("healthz = %d", got)
			}
			res := do(t, srv, "PUT", "/admin/v1/declarations", "t", propsDecl("com.example.sql."+backend))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("put = %d", res.StatusCode)
			}
			if _, err := a.Engine.GetDeclaration(context.Background(), "com.example.sql."+backend); err != nil {
				t.Fatal(err)
			}
			if err := a.Engine.DeleteDeclaration(context.Background(), "com.example.sql."+backend); err != nil {
				t.Fatal(err)
			}
		})
	}
}
