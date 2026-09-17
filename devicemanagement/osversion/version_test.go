package osversion_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
)

func TestParse(t *testing.T) {
	t.Parallel()
	good := map[string]osversion.Version{
		"26":      osversion.New(26, 0, 0),
		"26.4":    osversion.New(26, 4, 0),
		"10.15.4": osversion.New(10, 15, 4),
		" 1.1 ":   osversion.New(1, 1, 0),
		"0.0.1":   osversion.New(0, 0, 1),
	}
	for in, want := range good {
		got, err := osversion.Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "n/a", "1.2.3.4", "a.b", "-1.0", "1..2"} {
		if _, err := osversion.Parse(bad); !errors.Is(err, osversion.ErrVersion) {
			t.Errorf("Parse(%q) err = %v, want ErrVersion", bad, err)
		}
	}
	if osversion.MustParse("13.0").String() != "13.0" || osversion.New(1, 2, 3).String() != "1.2.3" {
		t.Error("String")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustParse did not panic")
		}
	}()
	osversion.MustParse("bad")
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()
	a, b := osversion.New(10, 15, 4), osversion.New(11, 0, 0)
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(a) != 0 {
		t.Error("major compare")
	}
	if osversion.New(1, 2, 0).Compare(osversion.New(1, 3, 0)) != -1 || osversion.New(1, 2, 5).Compare(osversion.New(1, 2, 4)) != 1 {
		t.Error("minor/patch compare")
	}
	if !(osversion.Version{}).IsZero() || osversion.New(0, 1, 0).IsZero() {
		t.Error("IsZero")
	}
}
