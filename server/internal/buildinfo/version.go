// Package buildinfo reports the reference server module's build version.
package buildinfo

import "runtime/debug"

// GoReleaser sets this symbol when packaging binaries from a checkout.
var releaseVersion string

// Version reports the release stamp, or the module version for go install builds.
func Version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	info, _ := debug.ReadBuildInfo()
	return moduleVersion(info)
}

func moduleVersion(info *debug.BuildInfo) string {
	if info == nil || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
