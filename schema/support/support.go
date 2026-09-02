package support

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// OS names as used in Apple's schema.
type OS string

// Operating systems.
const (
	IOS      OS = "iOS"
	MacOS    OS = "macOS"
	TvOS     OS = "tvOS"
	VisionOS OS = "visionOS"
	WatchOS  OS = "watchOS"
)

// AllOS lists the operating systems in schema order.
var AllOS = []OS{IOS, MacOS, TvOS, VisionOS, WatchOS}

// Channel is the MDM channel a message travels on.
type Channel string

// Channels.
const (
	ChannelDevice Channel = "device"
	ChannelUser   Channel = "user"
)

// Version is a dotted OS version. The zero Version means "unspecified".
type Version struct {
	Major, Minor, Patch int
}

// ErrVersion is returned by ParseVersion for malformed input.
var ErrVersion = errors.New("support: malformed version")

// ParseVersion parses "26", "26.4", or "10.15.4".
func ParseVersion(s string) (Version, error) {
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

// MustVersion parses or panics; for generated tables and tests.
func MustVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

// V builds a Version.
func V(major, minor, patch int) Version { return Version{major, minor, patch} }

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

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Mode is Apple's allowed/required/forbidden/ignored setting for shared
// iPad and user enrollment contexts. Empty means unspecified (allowed).
type Mode string

// Modes.
const (
	ModeAllowed   Mode = "allowed"
	ModeRequired  Mode = "required"
	ModeForbidden Mode = "forbidden"
	ModeIgnored   Mode = "ignored"
)

// OSSupport is the effective support block for one key on one OS after
// inheritance from the enclosing payload and parent keys.
type OSSupport struct {
	// NotAvailable is set when Apple lists "introduced: n/a".
	NotAvailable bool
	Introduced   Version
	Deprecated   Version
	Removed      Version
	AccessRights string
	Beta         bool

	// Tri-state booleans: nil means Apple did not say.
	Multiple           *bool
	DeviceChannel      *bool
	UserChannel        *bool
	Supervised         *bool
	RequiresDEP        *bool
	UserApprovedMDM    *bool
	AllowManualInstall *bool
	AlwaysSkippable    *bool

	AllowedEnrollments []string
	AllowedScopes      []string

	SharedIPadMode          Mode
	SharedIPadDeviceChannel *bool
	SharedIPadUserChannel   *bool
	SharedIPadScopes        []string

	UserEnrollmentMode     Mode
	UserEnrollmentBehavior string
}

// Entry is the support table row for one schema key path.
type Entry struct {
	Path string
	OS   map[OS]*OSSupport
}

// Target describes the device a value is destined for. The zero Target
// disables every context check.
type Target struct {
	OS             OS
	Version        Version
	Channel        Channel
	Supervised     bool
	SharedIPad     bool
	UserEnrollment bool
	DEP            bool
	UserApproved   bool
}

// Result of a support check.
type Result struct {
	Supported  bool
	Deprecated bool
	Reason     string
	OS         *OSSupport
}

// Bool is a helper for table literals.
func Bool(b bool) *bool { return new(b) }

// Check evaluates the entry against a target.
func (e *Entry) Check(t Target) Result {
	if e == nil {
		return Result{Supported: true, Reason: "no support data"}
	}
	if t.OS == "" {
		return Result{Supported: true, Reason: "no target OS"}
	}
	os, ok := e.OS[t.OS]
	if !ok || os == nil || os.NotAvailable {
		return Result{Reason: fmt.Sprintf("%s: not available on %s", e.Path, t.OS)}
	}
	r := Result{Supported: true, OS: os}
	if !t.Version.IsZero() {
		if !os.Introduced.IsZero() && t.Version.Compare(os.Introduced) < 0 {
			return Result{OS: os, Reason: fmt.Sprintf("%s: requires %s %s, target is %s", e.Path, t.OS, os.Introduced, t.Version)}
		}
		if !os.Removed.IsZero() && t.Version.Compare(os.Removed) >= 0 {
			return Result{OS: os, Reason: fmt.Sprintf("%s: removed in %s %s", e.Path, t.OS, os.Removed)}
		}
		if !os.Deprecated.IsZero() && t.Version.Compare(os.Deprecated) >= 0 {
			r.Deprecated = true
			r.Reason = fmt.Sprintf("%s: deprecated in %s %s", e.Path, t.OS, os.Deprecated)
		}
	}
	switch t.Channel {
	case ChannelDevice:
		if os.DeviceChannel != nil && !*os.DeviceChannel {
			return Result{OS: os, Reason: fmt.Sprintf("%s: not supported on the device channel on %s", e.Path, t.OS)}
		}
	case ChannelUser:
		if os.UserChannel != nil && !*os.UserChannel {
			return Result{OS: os, Reason: fmt.Sprintf("%s: not supported on the user channel on %s", e.Path, t.OS)}
		}
	}
	if os.Supervised != nil && *os.Supervised && !t.Supervised {
		return Result{OS: os, Reason: fmt.Sprintf("%s: requires supervision on %s", e.Path, t.OS)}
	}
	if os.RequiresDEP != nil && *os.RequiresDEP && !t.DEP {
		return Result{OS: os, Reason: fmt.Sprintf("%s: requires Automated Device Enrollment on %s", e.Path, t.OS)}
	}
	if os.UserApprovedMDM != nil && *os.UserApprovedMDM && !t.UserApproved {
		return Result{OS: os, Reason: fmt.Sprintf("%s: requires user-approved MDM on %s", e.Path, t.OS)}
	}
	if reason := modeCheck(e.Path, "shared iPad", os.SharedIPadMode, t.SharedIPad); reason != "" {
		return Result{OS: os, Reason: reason}
	}
	if reason := modeCheck(e.Path, "user enrollment", os.UserEnrollmentMode, t.UserEnrollment); reason != "" {
		return Result{OS: os, Reason: reason}
	}
	if t.SharedIPad {
		if t.Channel == ChannelDevice && os.SharedIPadDeviceChannel != nil && !*os.SharedIPadDeviceChannel {
			return Result{OS: os, Reason: fmt.Sprintf("%s: not supported on the device channel of a shared iPad", e.Path)}
		}
		if t.Channel == ChannelUser && os.SharedIPadUserChannel != nil && !*os.SharedIPadUserChannel {
			return Result{OS: os, Reason: fmt.Sprintf("%s: not supported on the user channel of a shared iPad", e.Path)}
		}
	}
	return r
}

func modeCheck(path, what string, m Mode, inEffect bool) string {
	switch m {
	case ModeRequired:
		if !inEffect {
			return fmt.Sprintf("%s: only valid when %s is in effect", path, what)
		}
	case ModeForbidden:
		if inEffect {
			return fmt.Sprintf("%s: not valid when %s is in effect", path, what)
		}
	case ModeAllowed, ModeIgnored, "":
	}
	return ""
}

var (
	mu       sync.RWMutex
	families = map[string]map[string]*Entry{}
)

// Register installs a family's table. Generated packages call it from init.
// Registering the same family twice replaces the table.
func Register(family string, table map[string]*Entry) {
	mu.Lock()
	defer mu.Unlock()
	families[family] = table
}

// Lookup returns the entry for a key path in a family, or nil.
func Lookup(family, path string) *Entry {
	mu.RLock()
	defer mu.RUnlock()
	return families[family][path]
}

// Families lists registered families in sorted order.
func Families() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(families))
	for f := range families {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Paths lists the key paths of a family in sorted order.
func Paths(family string) []string {
	mu.RLock()
	defer mu.RUnlock()
	t := families[family]
	out := make([]string, 0, len(t))
	for p := range t {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
