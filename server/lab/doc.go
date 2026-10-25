// Package lab runs the maintained acceptance modules against a workspace and a target.
//
// The package is importable: a separate device lab, with its own drivers, target matrix,
// Apple credentials and private evidence, registers its own modules against these types
// and reuses this runner, result model and report rather than reimplementing them.
//
// # Design
//
// A workspace keeps local identities and configuration across runs. Start can embed the
// server runtime or launch dmserver processes, using the same server configuration in
// either case. Up supervises those processes in the foreground; other commands attach
// through an authenticated loopback control endpoint.
//
// The catalogue is one ordered inventory of modules. A module declares the workspace modes
// whose server environment it needs, the target capabilities it uses and the lifecycle
// stage that orders it. Modes select the service environment: simulated workspaces provide
// local Apple-service fixtures, while live workspaces use the operator's Apple credentials
// and a real device. Targets, in package target, supply the device side. A run writes JSON,
// JUnit and a self-contained HTML report, distinguishing failures from unavailable
// prerequisites and inapplicable modules.
//
// Doctor and the offline preflight report missing prerequisites without enrolling a device
// or changing its trust settings.
//
// ADE modules separate read-only server-token/API evidence, explicitly opted-in
// designated assignment with private recovery fencing, and actual native enrollment
// proof. API and fixture passes cannot certify hardware. Per-platform results retain
// unexercised scope, and DDM exchange and cleanup have independent evidence.
// See docs/operations/ade-acceptance.md for private inputs and executable live steps.
//
// Apps and Books checks independently collect read-only location evidence,
// exercise explicitly designated free-app licensing through SDK and reference
// paths with private original-state fencing, and inspect actual native app
// installation/version. Licensing callbacks, aggregate status, per-target
// readback and cleanup are separate requirements. An inspection never removes
// a preexisting app or claims restoration after external preparation. User
// licensing retains Apple's clientUserId independently of hardware serials.
// See docs/operations/apps-and-books-acceptance.md for private inputs and scope.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/local_lab_acceptance_testing.md
package lab
