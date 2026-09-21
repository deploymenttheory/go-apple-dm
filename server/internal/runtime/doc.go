// Package runtime starts and stops the reference server's HTTP listeners and
// background workers.
//
// # Design
//
// Serve validates listener security, builds the configured application and runs
// it until cancellation or a serving failure. It supports configured certificate
// files and managed HTTPS identities. The unified reference server uses a local
// declaration engine. Plain HTTP is restricted to literal loopback addresses.
//
// ServeListener uses the same lifecycle with a caller-bound listener, retaining
// an ephemeral port throughout application setup. It owns closing the listener,
// including on startup failure, and validates the actual bound address.
//
// Shutdown stops HTTP admission and drains active requests before cancelling
// workers. A shared deadline bounds that drain, and workers that fail to stop
// are reported as an error. Serve closes the application resources it owns.
//
// # References
//
//   - Reference server: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/getting-started/reference-server.md
//   - Certificate lifecycle: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/certificate-lifecycle.md
package runtime
