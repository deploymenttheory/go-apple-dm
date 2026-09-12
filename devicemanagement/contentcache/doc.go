// Package contentcache decodes Apple Content Cache metrics and provides an
// embeddable receiver with caller-supplied authorization and storage.
//
// This package follows openapi/content-cache/metrics_report.json from
// apple/device-management commit b0180185a5e4077070710033341b71d0cbe1a18a.
// It is an opt-in seed contract, independent of the stable MDM schema pin.
// The upstream contract and license are retained in testdata.
//
// # Deployment
//
// Mount NewReceiver at the URL configured in ManagementStatusTarget. The caller
// configures HTTPS, verifies client credentials in Config.Authorize, and stores
// or forwards reports in Config.Accept. Middleware may attach authenticated
// identity to the request context. A 202 response means Accept succeeded; the
// library does not promise durable storage, retries or deduplication.
//
// Apple's OpenAPI specifies POST /metrics, while the DDM content-cache settings
// describe PUT to a configurable URL. The receiver accepts both methods. This
// accommodates the documented discrepancy; physical-device interoperability
// still requires verification. No dmserver route is installed by this package.
package contentcache
