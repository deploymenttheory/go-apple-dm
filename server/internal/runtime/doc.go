// Package runtime starts and stops the reference server's HTTP listeners and
// background workers.
//
// Serve validates listener security, builds the configured application and runs
// it until cancellation or a serving failure. It supports configured certificate
// files and managed HTTPS identities. Plain HTTP is restricted to loopback;
// the DDM role additionally requires an explicit test option.
//
// Shutdown stops HTTP admission and drains active requests before cancelling
// workers. A shared deadline bounds that drain, and workers that fail to stop
// are reported as an error. Serve closes the application resources it owns.
package runtime
