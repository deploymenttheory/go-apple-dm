module github.com/deploymenttheory/go-apple-dm

go 1.27.0

// Everything published under this path before v0.1.0 is withdrawn. v1.0.0 was
// tagged by release-please while the library was still being built, and
// nothing depended on it. The project restarts at v0.1.0, where a pre-1.0
// version says what is true: the API is not yet stable.
//
// v2.0.0 is absent because it cannot be named here and does not need to be.
// The go command rejects it -- "should be v0 or v1, not v2" -- for a module
// path with no /v2 suffix, and that missing suffix is the same reason the
// proxy never served it: its version list holds only v1.0.0.
retract (
	v1.0.1 // Retraction only; carries this block.
	v1.0.0 // Tagged before the library was ready.
)

require (
	github.com/micromdm/plist v0.3.0
	github.com/smallstep/pkcs7 v0.2.3
	github.com/smallstep/scep v0.0.0-20260331191114-261f960a40d1
	go.opentelemetry.io/otel v1.46.0
	go.opentelemetry.io/otel/metric v1.46.0
	go.opentelemetry.io/otel/trace v1.46.0
	golang.org/x/crypto v0.55.0
	gopkg.in/yaml.v3 v3.0.1
	howett.net/plist v1.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)
