// Package buildinfo reports the reference server module's build version.
//
// # Design
//
// Version uses the release version embedded when packaging a binary, then falls
// back to Go's module build information for go install builds. It returns devel
// when no version information is available.
//
// # References
//
//   - Go build metadata: https://pkg.go.dev/runtime/debug#ReadBuildInfo
//   - Release configuration: https://github.com/deploymenttheory/go-apple-dm/blob/main/CONTRIBUTING.md
package buildinfo
