package app

import (
	"net/url"
	"slices"
	"strconv"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// authoringTarget keeps Blueprint JSON deterministic while allowing explicit
// target validation through query parameters. An omitted target retains the
// existing structural validation; a supplied target requires OS/version/channel.
func authoringTarget(q url.Values) (support.Target, error) {
	var target support.Target
	if len(q) == 0 {
		return target, nil
	}
	target.OS = support.OS(q.Get("os"))
	target.Channel = support.Channel(q.Get("channel"))
	if !slices.Contains(support.AllOS, target.OS) || (target.Channel != support.ChannelDevice && target.Channel != support.ChannelUser) {
		return target, ddm.ErrInvalid
	}
	var err error
	target.Version, err = osversion.Parse(q.Get("version"))
	if err != nil || target.Version.IsZero() {
		return target, ddm.ErrInvalid
	}
	flags := map[string]*bool{"supervised": &target.Supervised, "sharedIPad": &target.SharedIPad, "userEnrollment": &target.UserEnrollment, "dep": &target.DEP, "userApproved": &target.UserApproved}
	for name, values := range q {
		if len(values) != 1 {
			return target, ddm.ErrInvalid
		}
		if name == "os" || name == "version" || name == "channel" {
			continue
		}
		flag, ok := flags[name]
		if !ok {
			return target, ddm.ErrInvalid
		}
		*flag, err = strconv.ParseBool(values[0])
		if err != nil {
			return target, ddm.ErrInvalid
		}
	}
	return target, nil
}
