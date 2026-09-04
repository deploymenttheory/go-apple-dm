# 0044: Repository layout — layered tiers and a separate reference-server module

Status: accepted
Date: 2026-09-04
Phase: post-9 (structural)

## Apple sources

None. This record is structural, not protocol. It changes where our packages live and what
they are allowed to import; it changes no wire format and no Apple-facing behaviour.

## References read

- `micromdm/nanomdm` top level: `api certverify cli cmd cryptoutil http mdm push service storage test tools`
- `jessepeterson/kmfddm` top level: `cmd ddm http jsonpath logkeys notifier storage test tools`
- `micromdm/nanodep` top level: `albc cli client cmd cryptoutil godep http log proxy storage sync tokenpki tools`
- `micromdm/micromdm` top level: `cmd dep mdm pkg platform server tools vpp workflow`, with
  `pkg/{activationlock,crypto,httputil}` and `platform/{apns,command,dep,device,profile,queue,...}`

## Known pitfalls found

- NanoMDM, KMFDDM and NanoDEP are all flat, and all are servers first. A flat tree costs them
  nothing because they publish a binary, not an API; nobody imports `nanomdm/storage` on purpose.
  We publish a library, so our tree is our API documentation, and 25 undifferentiated root entries
  say nothing about which package is the front door.
- MicroMDM is the only reference with an umbrella, and it is instructive in the opposite direction
  from the usual advice: `pkg/` holds three generic utilities, while the thirteen domain packages
  live under `platform/`. Its `pkg/` is a utility bucket, not a home for feature code.
- We had already drifted into the failure a flat tree invites: `ddm`, the marquee package, imported
  `service` and `storage`, so taking the declaration types meant taking the whole server stack.

## What they do

- **NanoMDM**: flat; `mdm/` holds the protocol core and is genuinely leaf-like, while `service/`
  and `storage/` sit beside it with no structural statement that one may not import the other.
  The separation is real but undocumented and unenforced.
- **KMFDDM**: flat; `ddm/` is the engine and `storage/` its persistence, coupled directly. Runs as
  a separate process, so the coupling never has to be defended.
- **MicroMDM**: `platform/` groups domains, `pkg/` holds utilities, `server/` is three files of
  wiring. The clearest layout of the four, and the only one that distinguishes domain from utility.

## What we do better

1. Tiers named for the role they play — `mdmprotocol/`, `pki/`, `appleplatformservices/`, `server/` — so an
   import path states which layer a package belongs to, and `schema/` stays at the root as the
   generated tree it is.
2. The layering is enforced, not asserted: `internal/layout` reads the tier from the import path
   and fails the build on any edge that points upwards. `depguard` was the obvious home for them and cannot be, because
   `.github/workflows/go-lint.yml` runs golangci-lint with `--issues-exit-code=0` and
   `only-new-issues`, so a lint rule reports a violation without failing anything. Tests are the
   only gate this repository actually has. None of the four references makes an enforced claim of
   any kind. To be precise about what this fixes: there is no Go import cycle here and never was
   — the module compiles, so package-level cycles are impossible. The tangle is at *directory*
   granularity, where `{ddm, dep, event, push, service, storage}` form a strongly connected
   component once subpackages are collapsed into their parent. That is what blocks tier
   assignment, because a directory cannot sit in two tiers: `event/sink` names the payload types
   of three domains while `event` itself depends only on `mdm`, and `storage` reaches into
   `push/pushcert` while `push` depends on `storage`. Both are fixed by moving the subpackage out
   of its parent, not by changing any dependency.
3. Pagination (`Page`, `Result[T]`, and the size bounds) moves to `paging/`. Three quarters of all
   references to `storage` from elsewhere in the module were these two value types, which put the
   declarative engine, the ACME server and the DEP client in the storage package's dependency graph
   for a struct with two fields. After the move `acme` and `dep` no longer import `storage` at all,
   and `ddm` does so in two files of sixteen.
4. The reference server becomes its own module, so "this is not a product" is structural rather
   than a promise in the README, and a consumer importing `protocol/mdm` no longer inherits `pgx`,
   `cedar-go`, `otel` and `modernc-sqlite` through a module-wide `go.sum`.

## Verified by

1. `make verify` stays green across every step, proving `schema/` was not disturbed.
2. `layout.TestPureTierCannotReachServerTier` holds twenty-four packages to the boundary and
   `layout.TestKnownServerTierReachIsExact` asserts the remaining five violations *exactly*, so a
   split that does not shrink the debt register fails as loudly as a regression that grows it.
   `layout.TestNoUnitCycles` proves every directory is still assignable to one tier, and
   `layout.TestPushcertImportsOnlyTheStandardLibrary` keeps
   `storage -> pushcert -> push -> storage` from becoming a genuine import cycle. The suite was
   verified to fail: one added import in `cms` failed five packages transitively.
3. `layout.TestTiersOnlyImportDownwards` reads every package's tier from its path and rejects any
   upward edge; `layout.TestEveryTierIsPopulated` stops a renamed tier directory from silently
   demoting everything under it and making that check vacuous. One exception is recorded and
   named: `mdmprotocol/enroll/ade` reads Apple's software lookup service to gate Automated Device
   Enrollment on OS version, so it imports `appleplatformservices/gdmf` for a one-method interface,
   a value type and a string comparison. The edge is nominal — `gdmf` imports nothing in this
   module — and the fix is a design choice left open: split gdmf's vocabulary from its HTTP client
   as push and pushnotify were split, invert at the ade boundary, or give enrollment its own tier.
   The coverage gate stays above 95% overall and per package throughout.
4. In a scratch module, `go get` of the library followed by importing only `protocol/mdm` yields a
   `go.sum` with no `pgx`, `cedar-go`, `otel` or `modernc-sqlite`.

## Rejected alternatives

- **Stay flat** (as NanoMDM, KMFDDM, NanoDEP): idiomatic Go and zero risk, and it is what the
  standard library does. Rejected because those projects publish binaries and we publish an API;
  a flat tree left `ddm` importing `service` with nothing to prevent it.
- **`pkg/` umbrella**: `pkg` carries no information and groups nothing — the same 23 siblings, one
  level deeper. MicroMDM's own use of `pkg/` for utilities only is the argument against using it
  for feature code. (`.gitignore` also ignored `/pkg/`, which would have hidden the tree.)
- **`devicemanagement/` umbrella**: named for Apple's documentation namespace, but it stutters
  against the `go-apple-dm` module path and still expresses no layering.
- **Group by subject without the refactor**: pure `git mv`, real readability gain, and rejected
  because it would make the tree look layered while `ddm -> service` and the directory-level
  `{ddm, dep, event, push, service, storage}` component persisted underneath. A hierarchy that asserts
  a boundary the compiler does not enforce is worse than a flat one, because it misleads.
- **Move `schema/` under `protocol/`**: rejected for cost with no design benefit. The generator
  emits `schema/...` import paths as string literals in eight places, so the move would require
  editing the emitters and regenerating, and CLAUDE.md's "generated code lives only under
  `schema/`" rule would need rewording to say the same thing.
