package osversion

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// Version is a dotted OS version. The zero Version means "unspecified".
type Version struct {
	Major, Minor, Patch int
}

// ErrVersion is returned by Parse for malformed input. The version usually arrives
// from an API caller, so the condition is catalogued for them.
var ErrVersion = fault.OSVersionMalformed

// Parse parses "26", "26.4", or "10.15.4".
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("%w: empty", ErrVersion)
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return Version{}, fmt.Errorf("%w: %q", ErrVersion, s)
	}
	var v Version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("%w: %q", ErrVersion, s)
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	return v, nil
}

// MustParse parses or panics; for generated tables and tests.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// New builds a Version.
func New(major, minor, patch int) Version { return Version{major, minor, patch} }

// IsZero reports whether the version is unspecified.
func (v Version) IsZero() bool { return v == Version{} }

// String implements fmt.Stringer.
func (v Version) String() string {
	if v.Patch != 0 {
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Compare returns -1, 0, or 1.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return cmp(v.Major, o.Major)
	case v.Minor != o.Minor:
		return cmp(v.Minor, o.Minor)
	default:
		return cmp(v.Patch, o.Patch)
	}
}

// cmp compares version components and returns their ordering.
func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
