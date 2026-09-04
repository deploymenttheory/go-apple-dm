package mysql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/mysql"
)

func TestOpenArgumentErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := mysql.Open(ctx, "", mysql.Options{}); !errors.Is(err, mysql.ErrDSNRequired) {
		t.Fatalf("empty DSN: %v", err)
	}
	if _, err := mysql.Open(ctx, "not a dsn ://", mysql.Options{}); err == nil || errors.Is(err, mysql.ErrDSNRequired) {
		t.Fatalf("bad DSN: %v", err)
	}
}

func TestNormalizeDSN(t *testing.T) {
	t.Parallel()
	n, err := mysql.NormalizeDSN("u:p@tcp(h:3306)/db")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"parseTime=true", "charset=utf8mb4", "clientFoundRows=true", "time_zone=%27%2B00%3A00%27"} {
		if !strings.Contains(n, want) {
			t.Errorf("normalized DSN %q lacks %s", n, want)
		}
	}
	if _, err := mysql.NormalizeDSN("://nope"); err == nil {
		t.Fatal("bad DSN normalised")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()
	dup := &gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
	if !mysql.IsUniqueViolation(dup) || !mysql.IsUniqueViolation(errors.Join(errors.New("wrapped"), dup)) {
		t.Fatal("duplicate entry not detected")
	}
	for name, err := range map[string]error{
		"nil":        nil,
		"other code": &gomysql.MySQLError{Number: 1146},
		"plain":      errors.New("x"),
	} {
		if mysql.IsUniqueViolation(err) {
			t.Errorf("%s reported as unique violation", name)
		}
	}
}
