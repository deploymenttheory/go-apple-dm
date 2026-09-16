package sqlstore_test

import "testing"

// requireType reports malformed fixture responses at the caller instead of panicking.
func requireType[T any](tb testing.TB, value any) T {
	tb.Helper()
	result, ok := value.(T)
	if !ok {
		tb.Fatalf("unexpected type %T; want %T", value, result)
	}
	return result
}
