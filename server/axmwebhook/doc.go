// Package axmwebhook composes signed AXM callback receipt with durable inventory
// refresh. The host supplies an encrypted inventory backend, HTTP routing,
// administration and worker supervision. Receipt and pending work commit together;
// Apple callbacks are retained separately from subsequently fetched evidence.
// Subscription provisioning is explicit and uncertain POST outcomes are never
// automatically resubmitted. The package does not implement outbound native webhooks.
package axmwebhook
