// Package buildinfo reports the reference server module's build version.
//
// Version uses the release version embedded when packaging a binary, then falls
// back to Go's module build information for go install builds. It returns devel
// when no version information is available.
package buildinfo
