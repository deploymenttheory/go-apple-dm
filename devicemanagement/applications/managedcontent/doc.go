// Package managedcontent composes explicit Apps and Books licensing with MDM
// commands or declarative managed-app publication over caller-owned atomic state.
//
// # Operations and evidence
//
// Assign, Install and AssignAndInstall persist queued intent without network or
// delivery calls. Applications can commit that intent with required audit events
// using their existing state transaction boundary. Step resolves the explicitly
// selected authenticated enrollment, checks native asset capabilities, and requires
// a complete current assignment read before dispatch. Existing licenses do not
// require an artificial historical licensing job. User licensing retains Apple's
// clientUserId and remains usable without physical serial disclosure.
//
// A new explicit reinstall/update can name Plan.PredecessorID after predecessor
// completion has been observed. Promotion requires fresh authoritative terminal
// device evidence and atomically fences the previous worker. MDM gets a new
// command identity; DDM reuses its owned configuration and activation identities.
// A never-submitted successor can cancel and restore predecessor ownership with
// monotonically increasing fences; pending/uncertain work and method switches
// cannot use this path. Current Installed may be false after removal even though
// the predecessor's device processing was terminal.
//
// Ordinary operations expose safe status. LoadEvidence separately exposes private
// source pages, configuration, dispatch payloads and device/adapter evidence.
// Records do not expire; callers own authorization, encryption and retention.
//
// # Delivery ownership and recovery
//
// Delivery adapters atomically bind stable identities to immutable payloads and
// targets, support authoritative readback, and prevent competing management paths.
// Queued operations reserve native scope; physical scope is bound only after
// authenticated serial resolution. Unknown dispatches never automatically replay.
// Reconcile requires a stopped original worker, an expired lease and an operator
// note. Explicit retry additionally requires an authoritative, owned, atomically
// idempotent same-identity adapter guarantee. A previously observed dispatch cannot
// be replayed merely because a queue record was subsequently pruned.
//
// Command acknowledgment, declaration publication and device-reported installation
// remain separate. Offline devices can have dispatched work without installation.
//
// # Platform contracts
//
// Generated Apple support metadata validates OS version, enrollment and channel.
// tvOS supports the MDM application command, not the AppManaged declaration.
// InstallMedia retains its narrower book/platform rules, including removal on
// macOS 11 and later. macOS device licenses use the device channel; user licenses
// use the user channel. Declarative app publication includes a stable managed-app
// configuration and activation; it never infers that the device applied them.
//
// See Apple's Install Application, Install Media, AppManaged and managing-assets
// documentation, and decision 0053 for the surrounding licensing contract:
//
// https://developer.apple.com/documentation/devicemanagement/install-application-command
// https://developer.apple.com/documentation/devicemanagement/install-media-command
// https://developer.apple.com/documentation/devicemanagement/appmanaged
// https://developer.apple.com/documentation/devicemanagement/managing-assets
package managedcontent
