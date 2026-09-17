# Documentation

- [Getting started](getting-started/getting-started.md): choose the reference-server or custom-server path.
- [Run the reference server](getting-started/reference-server.md): Compose startup, CLI checks, stored administration and first-device enrollment.
- [Build your own server](getting-started/build-your-own-apple-dm-server.md): runnable Go examples, composition choices and caller responsibilities.
- [Configuration explained](getting-started/configuration.md): server JSON, secret/path precedence, CLI contexts and bench configuration.
- [Server release downloads](operations/server-releases.md): platform archives, signature verification and release maintenance.
- [Architecture](architecture.md): implemented capabilities, module boundaries and limitations.
- [OS versions and API migration](operations/os-versions.md): shared version primitives, feature availability and migration from the support version API.
- [Design decisions](research/decisions/README.md): current design and supporting evidence.
- [Diagrams](diagrams/README.md): interactive architecture, protocol and lifecycle views.
- [Enrollment security operations](operations/enrollment-security.md): trust, persistence, revocation and rate-limit configuration.
- [Event delivery](operations/event-delivery.md): SQL event capture, audit/webhook delivery, inspection and retry.
- [Status and profile inspection](operations/status-and-profile-inspection.md): paginated DDM diagnostics and offline profile lint.
- [Protocol helpers](operations/protocol-helpers.md): JWTs, password hashes, automatic encryption certificates, recovery-key decryption, bypass codes and installation manifests.
- [Apps and Books](operations/apps-and-books.md): location setup, device/user licensing, user association, notifications and completion checks.
- [CI responsibilities](testing/ci.md): triggers, distinct checks, dependency retries and release gates.
- [Certificate lifecycle](operations/certificate-lifecycle.md): provisioning, renewal, issuer rollover and retained encryption identities.
- [Backup and recovery](operations/recovery.md): maintenance fences, authenticated backups, verification and isolated restore.
- [Threat model](security/threat-model.md): assets, trust boundaries, controls and residual risks.
- [Reference-server bench](testing/bench.md): shared scenarios, acceptance, contracts and live execution.
- [Bench API and configuration](operations/reference-bench.md): runtime, enrollment and app push additions.
- [Test scenarios](testing/e2e-scenarios.md): executable simulator scenarios and their limits.
- [Mac enrollment testing](operations/mac-enrollment-testing.md): physical-device prerequisites, installation and acceptance checks.

- [Apple OS 27 coverage](operations/apple-os27-coverage.md): feature mapping, macOS 26 compatibility and content-cache operations.
- [macOS 27 handoff](testing/macos27-handoff.md): prepared code, pre-upgrade evidence and post-reboot acceptance.
- [Physical macOS 27 validation](testing/macos27-live-validation.md): observed device results, fixes and remaining acceptance.
