# 0031: ACME server, client identifiers, and the ACME state store

Status: accepted
Date: 2026-09-02
Phase: 7

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
  (`DirectoryURL`, and `ClientIdentifier`: "A unique string identifying a specific device. The
  server may use this as an anti-replay code to prevent issuing multiple certificates. This
  identifier also indicates to the ACME server that the device has access to a valid client
  identifier issued by the enterprise infrastructure. This can help the ACME server determine
  whether to trust the device. Though this is a relatively weak indication because of the risk
  that an attacker can intercept the client identifier.")
- Doc: <https://developer.apple.com/documentation/devicemanagement/identity-management>
- YAML: `third_party/device-management/mdm/profiles/com.apple.security.acme.yaml`
  (the order uses the `ClientIdentifier` as the `permanent-identifier`; the challenge type is
  `device-attest-01`; the certificate request carries the `ClientIdentifier`, `Subject`,
  `SubjectAltName`, `UsageFlags` and `ExtendedKeyUsage`, and "the ACME server may override or
  ignore" the Subject and SubjectAltName)
- YAML: `third_party/device-management/declarative/declarations/assets/credentials/acme.yaml`
  (the same flow for a declarative credential, where `ClientIdentifier` is described as "a
  one-time code to prevent issuing multiple certificates")

## References read

- `brandonweeks/nanoca@df2dba6c` `ca.go`, `handlers.go`, `jose.go`, `storage.go`, `machine.go`,
  `memory.go`, `problems.go` (the endpoint set, the JWS structural checks that go-jose does not
  make, the reservation and lease state machine, the storage contract, and re-verification of
  the stored attestation at finalize)
- `smallstep/certificates@bb481fbf` `acme/api/middleware.go`, `acme/api/order.go`,
  `acme/order.go`, `acme/db/nosql/nonce.go`, `acme/errors.go`,
  `authority/provisioner/acme.go` (the accepted algorithm set, the `url` header check, nonce
  handling, the finalize checks, and the problem type table)
- `fleetdm/fleet@e1bbd21c` `server/mdm/acme/internal/service/`, `server/mdm/acme/api/http/`,
  `articles/testing-apple-device-attestation-without-a-commercial-ca.md` (per-enrollment
  scoping, the order validation rules, and the field report on nanoca behind a TLS-terminating
  proxy)
- `hslatman/ios-acme-simulator@8373a8f9` `main.go` (the client side of the exchange)
- Records 0008 (certificate authority abstraction), 0026 (the store shape this one follows), and
  0032 (attestation verification, which this server calls).

## Known pitfalls found

- `smallstep/certificates` `acme/db/nosql/nonce.go`: nonces have no expiry. `dbNonce` declares
  `DeletedAt` and never sets it, there is no TTL on the bucket and no sweeper, so the table grows
  for the life of the deployment and a nonce minted a year ago is still usable.
- `smallstep/certificates` `acme/api/middleware.go:99`: the request body is read with
  `io.ReadAll` and no limit. The device-attest-01 payload carries a whole attestation object, so
  this is the one ACME endpoint where an unbounded body is easy to abuse.
- `smallstep/certificates` `acme/api/middleware.go:189-199`: the expected `url` is built as
  `https://` plus `r.Host` plus the path. Behind a proxy that rewrites Host the comparison fails,
  and there is no `X-Forwarded-*` handling anywhere in the repository.
- `brandonweeks/nanoca` `jose.go`: `validateRequestURL` requires `r.TLS != nil`. Fleet's write-up
  documents the consequence: on a host that terminates TLS at the edge "every signed ACME POST
  comes back as `HTTPS is required`".
- `smallstep/certificates` `acme/challenge.go:906`: the ordered identifier must equal the
  attested UDID or the attested serial number. That makes the client identifier the device's
  serial number, which is printed on the case and appears in every inventory, so it carries no
  secret at all.
- `brandonweeks/nanoca` `handlers.go`: the ordered identifier is never compared with the
  attestation, and no client identifier is ever consumed, so one identifier buys any number of
  certificates.
- `smallstep/certificates` `acme/errors.go:238-242` and `:99-100`: `notImplemented` renders the
  URN of `rejectedIdentifier`, and `invalidContact.String()` returns `"incorrectResponse"`. Two
  problem documents therefore name the wrong error.
- `brandonweeks/nanoca` `handlers.go`: an authorizer error settles the challenge back to pending
  and answers 500, which is right, and is the behaviour we copy. step-ca has no such seam, so
  every refusal there is terminal whether or not it was a device's fault.

## What they do

- **nanoca**: handler-only ACME with pluggable storage, issuer, authorizer and verifier;
  flattened JWS with hand-written structural checks around go-jose; ES256 only; nonces taken
  atomically with an hour's expiry; orders accept `permanent-identifier` and `hardware-module`;
  one `device-attest-01` challenge per identifier; a reservation with a lease makes challenge
  validation and finalize idempotent under concurrent posts; the raw attestation is stored and
  re-verified at finalize; issuance derives the certificate's SANs from the attestation.
- **step-ca**: ACME as one provisioner of a general CA; RSA, ECDSA and EdDSA algorithms; nonces
  atomic but eternal; order identifiers validated only for emptiness; the attested key's
  fingerprint is carried on the authorization and compared with the certificate request at
  finalize when present; the certificate's SAN comes from the order identifier; attestation
  roots and formats are provisioner settings; no per-device policy.
- **Fleet**: ACME scoped per enrollment under `/api/mdm/acme/{identifier}/`; ECDSA only; nonces
  in Redis; one identifier per order which must equal the host identifier; the certificate
  request's common name must equal it too and the key must be ECDSA; issuance gated on the
  serial having a device enrollment service assignment; the certificate subject is rewritten by
  the server.

## What we do better

1. The client identifier is a bearer token that carries its own binding, not the device's serial
   number. It is minted under an HMAC with an expiry and the device it was issued for, so
   knowing a serial number is not enough to order a certificate, and the server needs no state
   between composing an enrollment profile and seeing the order that uses it.
2. Apple's anti-replay semantics are enforced. The first order to present a client identifier
   claims it in the same transaction that creates the order, so a second order for the same
   identifier is refused even when the two race. Neither nanoca nor step-ca consumes the
   identifier at all.
3. The expected `url` is the URL the directory published, taken from the configured base, rather
   than reconstructed from the request's Host and TLS state. A deployment behind a
   TLS-terminating proxy works with no forwarded header to be believed, which is the failure
   nanoca hits and step-ca cannot express.
4. Nonces expire and are pruned. A nonce carries the time it was issued, a nonce older than the
   configured lifetime is refused, and `Prune` removes them along with expired orders,
   authorizations and challenges.
5. Every request body is bounded before it is parsed, and the JWS layer bounds it again.
6. A refusal is distinguished from a fault. A policy or lookup error that is not the client's
   leaves the challenge pending and answers 500, so a device is not permanently rejected because
   a directory was briefly unreachable; a client's error settles the challenge, its
   authorization and its order invalid in one transaction. A bad certificate request leaves the
   order ready, as RFC 8555 section 7.4 requires, so an amended request can still finalize it.
7. Only implemented endpoints are advertised. The directory omits `revokeCert` and `keyChange`
   because Apple's client never calls them, and an endpoint that is never exercised is one whose
   faults are never found.
8. The store is one contract with four implementations. In-memory, SQLite, PostgreSQL and MySQL
   all run the same suite, including the byte-exact round trip of the stored attestation that
   finalize depends on, and a concurrency test that asserts exactly one caller can take a nonce.

## Verified by

1. `acme.TestHMACIdentifiers` and `acme.TestHMACIdentifiers/Tampered` (prove claim 1: an
   identifier that was not minted by this server, or whose binding was edited, is rejected;
   step-ca's rule accepts any string equal to the device's serial number).
2. `acme.TestNewOrder/IdentifierIsOneTime` and `acme.TestNewOrder/ConcurrentOrdersClaimOnce`
   (prove claim 2; both would pass on nanoca and step-ca, which never consume the identifier).
3. `acme.TestSignedRequest/URLHeaderIsThePublishedURL` and `/ProxyHostIsIgnored` (prove claim 3;
   the second serves the same handler under a different Host and shows the request still
   succeeds, which nanoca and step-ca both fail).
4. `acme.TestNonce/SingleUse`, `/Expired`, and `acmetest.RunAll/Prune` (prove claim 4; step-ca
   has no expiry to test).
5. `acme.TestSignedRequest/BodyTooLarge` (proves claim 5).
6. `acme.TestChallenge/PolicyFailureLeavesTheChallengePending`,
   `/PolicyRefusalSettlesInvalid`, and `acme.TestFinalize/BadCSRLeavesTheOrderReady` (prove
   claim 6).
7. `acme.TestDirectory` (proves claim 7: the directory names exactly the endpoints that answer).
8. `acmetest.RunAll` run by `inmem.TestStore`, and `sqlstore.TestStore` on SQLite, PostgreSQL and
   MySQL, including `RunAll/Challenges/AttestationRoundTripsExactly` and
   `RunAll/Nonces/OnlyOneTakerWins` (prove claim 8).

## Rejected alternatives

- A JOSE module (`go-jose`, which nanoca uses): the plan of record admits no new module
  dependencies, and the JWS this server accepts is one serialisation with four header members.
  `acme/jose` is that, fuzzed, and it is where the interop fix for Apple's short ECDSA
  signatures lives.
- Writing our own ACME client for the simulator: `golang.org/x/crypto/acme` is available, and its
  `Challenge.Payload` field carries the attestation object, so the simulator drives the server
  with an independent implementation of RFC 8555. A server tested only against its own client
  proves much less.
- Making the client identifier the device serial number, as step-ca requires: it turns a value
  Apple calls an anti-replay code into public information.
- Per-enrollment ACME paths, as Fleet uses: it scopes state neatly but needs an enrollment to
  exist before a certificate can be issued, which forecloses using ACME for the first identity a
  device ever gets.
- A reservation and lease on challenge validation, as nanoca has: our validation is one
  transaction with no external call after the policy hook, so a repeated post finds the
  challenge already settled and returns its state. The lease would buy idempotency we already
  have.
- Supporting `hardware-module` identifiers: Apple orders with a `permanent-identifier` and
  nothing else, and an identifier type with no client is untested surface.
