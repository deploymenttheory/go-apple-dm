# 0035: `mdmctl` structure, output, and credential handling

Status: accepted
Date: 2026-09-02
Phase: 8

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
  (`mdmctl commands send` composes these)
- Doc: <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>
  (`mdmctl declarations` manages these)
- YAML: `third_party/device-management/mdm/commands/*.yaml` (the payloads `commands send` builds and
  the generated `Validate` it runs before the server sees them)

Framing: the CLI speaks only to our own admin API (0034). Apple defines nothing here; the reference
is what other MDM admin CLIs got right and wrong.

## References read

- `macadmins/nanohubctl@f88ef9a6e147eb23ed5b81068e2a3121fad58ba3` all 30 files, particularly
  `internal/cli/new.go`, `internal/cli/ddm/{helpers,declarations}.go`, `internal/utils/cobra.go`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `cmd/mdmctl/` (`mdmdctl.go`,
  `config.go`, `get.go`, `get_users.go`, `apply.go`), `pkg/httputil/httputil.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` `tools/*.sh` (18 curl scripts),
  `tools/syncdir.py`, `tools/ideclr.py`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `cmd/nano2nano/main.go`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `server/service/middleware/auth/api_only.go`
  (the endpoint-restricted API-only user, the closest thing to a scoped CLI credential)
- Ours: `cmd/mdmserver/main.go` (the testable-`run` shape), `cmd/admgen/main.go` (subcommand
  dispatch), `scripts/coverage-gate.sh`, `scripts/coverage-exempt.txt`

## Known pitfalls found

- `micromdm/micromdm` `cmd/mdmctl/config.go:239,243`: the API token is written in cleartext to
  `~/.micromdm/<name>.json`; the file is `0600` but the directory is created `0777`, and
  `mdmctl config print` echoes the token to stdout.
- `micromdm/micromdm` `cmd/mdmctl/config.go:27`: a `skip_verify` flag becomes
  `tls.Config{InsecureSkipVerify: true}` with no warning at use.
- `micromdm/micromdm` `cmd/mdmctl/get.go:45`, `config.go:38`: `os.Exit(1)` from inside subcommand
  `Run` methods, so nothing below `main` is testable and every failure is exit code 1.
- `micromdm/micromdm` `cmd/mdmctl/get.go`: no pagination — `getDevices` pulls the whole fleet into
  memory — and no machine-readable output mode at all, so the tables cannot be scripted.
- `macadmins/nanohubctl` `internal/cli/new.go`: writes the API key in plaintext to
  `~/.nanohubctl/config.json` and then prints it to stdout.
- `macadmins/nanohubctl` `internal/cli/ddm/helpers.go:157-163`: all five request helpers discard the
  error from `http.NewRequest` by shadowing it, so a nil request can reach `Do`.
- `macadmins/nanohubctl` `internal/cli/ddm/declarations.go:29`: `allDecls, nil := ...` assigns the
  error to the shadowable identifier `nil` and drops it.
- `macadmins/nanohubctl` `internal/utils/cobra.go`: credentials come from a package-level viper
  singleton, so no command is testable without mutating global state. 30 files, zero tests.
- `macadmins/nanohubctl` `internal/utils`: output is re-rendered with `json.MarshalIndent`, so key
  order and number formatting from the server are lost for `jq` consumers, and the pretty-printer
  swallows its own error.
- `jessepeterson/kmfddm` `tools/api-*.sh`: 18 scripts each re-implementing Basic auth from
  `$API_USER`/`$API_KEY`, so the credential lives in shell history and process listings.
- `micromdm/nanomdm` `cmd/nano2nano/main.go`: streams migration check-ins one at a time, logging and
  continuing on every failure, with no resume and exit 0 on partial failure.
- Ours: `scripts/coverage-gate.sh:92` computes the overall figure from every package in the merged
  profile, and `make test` runs `-coverpkg=./...`, so `cmd/` statements count toward the total even
  though `scripts/coverage-exempt.txt` exempts the package. The exemption suppresses the per-package
  FAIL line only. `cmd/mdmserver/main.go` is already 109 lines against the plan's "<100".

## What they do

- **micromdm `mdmctl`**: `flag` only, an `os.Args[1]` switch, `usageFor` rendering flags through
  `text/tabwriter`, and per-resource table writers with `BasicHeader`/`BasicFooter`. The best output
  of the three. Config is a JSON file of named servers holding live tokens; `Run` methods exit
  directly; no pagination, no JSON mode, no exit codes.
- **nanohubctl**: cobra plus viper plus `github.com/google/logger`, ~50 transitive dependencies
  including `go-git` and `jennifer`. Credentials in a global singleton and on disk in cleartext.
  Errors dropped in every request helper. No pagination.
- **kmfddm**: no CLI at all — 18 `curl` scripts plus two Python helpers. `syncdir.py` is genuinely
  good: it walks a directory, uploads each declaration with `nonotify=1`, then issues a single
  notify for everything that changed.
- **Fleet**: no first-party CLI in this repo's sense, but `fleetctl` authenticates as an API-only
  user whose token is a non-expiring session, optionally restricted to a whitelist of
  `(method, route)` fingerprints — where an *empty* whitelist means unrestricted.

## What we do better

1. The CLI's logic is gated at 95%, not hidden behind the `cmd/` exemption. `cmd/mdmctl/main.go` is
   a single function that parses argv and calls `mdmctl.Run`; everything else lives in
   `internal/mdmctl`, `internal/mdmctl/adminclient` and `internal/mdmctl/explain`. This is forced by
   arithmetic, not taste: exempt packages still contribute statements to the overall figure, so a
   3000-line uncovered `cmd/mdmctl` would fail `make ci` outright. A test parses the exempt file and
   fails when it grows a second function. micromdm's `cmd/mdmctl` is 3185 lines with three trivial
   tests; nanohubctl has none.
2. The config file stores a reference to a credential, never the credential: `token_env` or
   `token_file`. Inlining one requires an explicit flag and prints a warning, the file is written
   `0600` in a `0700` directory, reading refuses a group- or other-readable file, and no subcommand
   exists whose job is printing the secret. A token is never accepted as a positional argument,
   because argv is world-readable in `ps`.
3. Machine-readable output is the server's bytes, unmodified. `-output json` writes the response
   body verbatim, so canonical JSON, key order and number formatting survive to `jq`; `-output
   ndjson` streams one item per line and composes with `-all` and with `migrate import`.
   nanohubctl re-indents and destroys both.
4. Pagination is first class. `adminclient.Each` follows `NextCursor` to exhaustion under `-all`;
   without it the CLI prints one page and writes the next cursor to **stderr**, so stdout stays
   machine-clean. None of the three references paginates.
5. Failures are distinguishable. Documented exit codes — 0 success, 1 request failed, 2 usage, 3
   partial success, 4 authentication or authorization — with the failing target reasons on stderr.
   Both reference CLIs exit 1 from arbitrary depth.
6. A 404 explains itself. The CLI asks `GET /config` once and, when the family is absent, says which
   role serves that route, turning the split-deployment role model from a bare 404 into a sentence.
   No reference has roles.
7. `commands send` builds and validates locally. Payloads come from `@file`, a literal, repeated
   `-set key=value` typed by the schema, or stdin; `-dry-run` composes the command with
   `mdm.NewCommand` and prints the plist without contacting a server. Targets are parsed into
   `mdm.EnrollmentID` the same way `mdm.Enrollment.Resolve` composes them, so an id the CLI builds
   is byte-identical to one the server stored.

## Verified by

1. `mdmctl.TestCmdMainStaysThin` (parses `cmd/mdmctl/main.go` with `go/parser` and fails on a second
   function or excess length), plus `internal/mdmctl` and its two subpackages passing the 95% gate
   (prove claim 1; would fail on micromdm and nanohubctl, whose CLI logic is untestable in `cmd/`
   and behind a global singleton respectively).
2. `mdmctl.TestConfig/NeverWritesTokenByDefault`, `/RefusesWorldReadable`, `/RefusesGroupReadable`,
   `/InlineRequiresFlagAndWarns`, `/TokenNotAPositionalArgument` (prove claim 2; would fail on
   micromdm, which writes cleartext under a `0777` directory, and on nanohubctl, which additionally
   prints the key).
3. `mdmctl.TestOutput/JSONIsVerbatim`, `/NDJSONStreamsItems` (prove claim 3; would fail on
   nanohubctl because `MarshalIndent` re-renders the body).
4. `mdmctl.TestPagination/FollowsCursorWithAll`, `/CursorGoesToStderr` (prove claim 4; would fail on
   all three references, none of which paginate).
5. `mdmctl.TestExitCodes/Usage`, `/Auth`, `/Partial`, `/RequestFailed` (prove claim 5; would fail on
   micromdm and nanohubctl, which `os.Exit(1)` or `log.Fatal` from inside subcommands).
6. `mdmctl.TestNotFoundExplainsRole` (proves claim 6).
7. `mdmctl.TestCommandsSend/PayloadFromFile`, `/PayloadFromSet`, `/PayloadFromStdin`,
   `/DryRunNeedsNoServer`, `/TargetSyntaxMatchesResolve` (prove claim 7; `nano2nano` and the kmfddm
   scripts have no local validation at all).

Failing-path coverage per the repo rule: `adminclient.TestDo/Timeout`, `/BadJSON`,
`/NonJSONErrorBody`, `/RedirectRefused`, `/BodyTooLarge`; `mdmctl.TestGlobalFlagsAfterVerb` (the
`flag` package stops at the first non-flag argument, so the verb's flag set is seeded with the same
global variables and both orderings must agree); `mdmctl.TestEveryVerbHasHelp`.

End to end: `e2e.TestE2E_AdminCLI` (E2E-024).

## Rejected alternatives

- cobra, urfave/cli, or viper (nanohubctl uses two of the three): a new module dependency, which
  records 0031 and 0032 refused on principle. `flag` plus a dispatch map is enough for a verb tree,
  and `cmd/admgen` already sets the precedent in this repo.
- A public `mdmctl` or `adminclient` package: phase 10 freezes the public API for two minor
  releases, and the admin routes are being redesigned in this same phase. Promoting the client is a
  phase 10 decision once the routes have settled.
- One package instead of three: `.golangci.yml` enables `gocyclo`, and three packages give three
  independent 95% targets with small fakes rather than one large package where an uncovered branch
  is hard to find. `explain` in particular has no I/O and can be finished before any API work lands.
- Storing the token in the config file by default (micromdm, nanohubctl): the file is the thing that
  leaks, and a reference to an environment variable or a file costs the operator nothing.
- Re-rendering JSON for readability: the human table already exists for humans; `-output json` is
  for machines and must not reshape the payload.
- Porting kmfddm's `syncdir.py` as `declarations sync <dir>`: genuinely useful and out of scope
  here; it needs the `nonotify` semantics our admin API does not yet expose. Phase 9 or later.
- Shell completion, colour, and TTY detection: deferred; none of it is needed to drive the API.
