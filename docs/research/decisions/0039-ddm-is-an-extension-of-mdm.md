# 0039: Declarative device management within the MDM enrollment

## Context

Declarative device management uses the existing MDM enrollment, identity and
check-in transport. Declaration evaluation is a separate library responsibility.

## Decision

The protocol core decodes `DeclarativeManagement` as a check-in message. The
service dispatches it through `DMHandler`; the declaration engine depends on MDM
types without a reverse engine dependency in the core. The reference server
assembles MDM and DDM together through the in-process adapter.

Declaration changes persist transactionally. The notifier creates ordinary
`DeclarativeManagement` commands with the current tokens and a deduplication key
containing the declarations-token digest. A newer token can queue while an older
command awaits acknowledgment or NotNow retry. Administrative mutations wake the
notifier; the engine has no callback into command dispatch.

## Rationale and constraints

One application database owns enrollment, inventory and protocol state. The DDM
SQL store wraps that pool. Target inventory is populated by enrollment and tracked
command responses; it is not copied between independent device inventories.
Storage interfaces and tables retain their own responsibilities.

Reusable proxy adapters support custom compositions. Their caller supplies shared
state and authenticated transport; the reference binary does not configure a
separate declaration server. Adapter wire behavior is documented in
[0023](0023-ddm-adapters-and-wire-contract.md).

Apple's command can carry tokens in `Data`. A client requests `tokens` when they
are absent; an unchanged declarations token needs no declaration synchronization.
The simulator implements that flow and fetches tokens again when resolving a
manifest conflict. Server publication is distinct from device application.

## Verification

Layout tests enforce dependency direction. Service and application tests verify
normal command events, target checks and notifier wakeups. Notifier regressions
exercise coalescing, retries and delivery of a newer generation with older work
pending; simulator tests verify command-embedded tokens and convergence.

## References

- [Notifier](../../../server/ddmsync/notifier.go)
- [Simulator synchronization](../../../devicemanagement/simulator/ddm.go)
- [In-process adapter](../../../server/ddmadapter/inproc)
- [Apple DDM integration](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management)
- [Apple DeclarativeManagement command](https://developer.apple.com/documentation/devicemanagement/declarativemanagementcommand)
