package explain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

// ErrTarget is a malformed -target value.
var ErrTarget = errors.New("explain: bad target")

// ParseTarget reads a target expression:
//
//	macos:15.0
//	ios:18,channel=user,supervised
//	macos:26.4,channel=device,supervised,dep,user-approved
//
// The first field is the OS, optionally with a version; the rest are
// comma-separated flags, or channel=device|user.
func ParseTarget(s string) (support.Target, error) {
	var t support.Target
	s = strings.TrimSpace(s)
	if s == "" {
		return t, nil
	}
	fields := strings.Split(s, ",")

	osName, version, _ := strings.Cut(strings.TrimSpace(fields[0]), ":")
	matched := false
	for _, known := range support.AllOS {
		if strings.EqualFold(string(known), osName) {
			t.OS, matched = known, true
			break
		}
	}
	if !matched {
		return support.Target{}, fmt.Errorf("%w: OS %q (want one of %s)", ErrTarget, osName, osNames())
	}
	if version != "" {
		v, err := support.ParseVersion(version)
		if err != nil {
			return support.Target{}, fmt.Errorf("%w: version %q: %w", ErrTarget, version, err)
		}
		t.Version = v
	}

	for _, f := range fields[1:] {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if ch, ok := strings.CutPrefix(f, "channel="); ok {
			switch ch {
			case "device":
				t.Channel = support.ChannelDevice
			case "user":
				t.Channel = support.ChannelUser
			default:
				return support.Target{}, fmt.Errorf("%w: channel %q (want device or user)", ErrTarget, ch)
			}
			continue
		}
		switch f {
		case "supervised":
			t.Supervised = true
		case "shared-ipad", "sharedipad":
			t.SharedIPad = true
		case "user-enrollment", "userenrollment":
			t.UserEnrollment = true
		case "dep":
			t.DEP = true
		case "user-approved", "userapproved", "uamdm":
			t.UserApproved = true
		default:
			return support.Target{}, fmt.Errorf("%w: unknown flag %q", ErrTarget, f)
		}
	}
	return t, nil
}

func osNames() string {
	names := make([]string, 0, len(support.AllOS))
	for _, os := range support.AllOS {
		names = append(names, string(os))
	}
	return strings.Join(names, ", ")
}
