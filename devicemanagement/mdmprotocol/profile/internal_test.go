package profile

import "testing"

func TestNumForms(t *testing.T) {
	t.Parallel()
	for _, v := range []any{int64(2), uint64(2), 2, 2.0} {
		if got := num(map[string]any{"k": v}, "k"); got != 2 {
			t.Fatalf("%T: %d", v, got)
		}
	}
	if num(map[string]any{"k": "x"}, "k") != 0 || num(map[string]any{}, "k") != 0 {
		t.Fatal("non-numeric")
	}
}
