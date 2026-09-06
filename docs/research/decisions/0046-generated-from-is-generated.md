# 0046: GENERATED_FROM.json is generated, and the commit comes from the checkout

Status: accepted
Date: 2026-09-04
Phase: post-9 (generator)

## Apple sources

- YAML: `third_party/device-management/**` (the tree being described)
- Doc: <https://developer.apple.com/documentation/devicemanagement>

## References read

- `korylprince/go-adm`: generates from the same Apple YAML; records no upstream commit at all
- `deploymenttheory/go-sdk-appleservices`: regenerated weekly with no record of what from
- `micromdm/nanomdm`: hand-written types, so nothing to pin

## Known pitfalls found

- **This repository, before this record.** `GENERATED_FROM.json` was hand-maintained, and
  `cmd/admgen` read the commit *out of that file* to stamp the generated doc comments. Bumping the
  submodule and regenerating therefore produced a tree built from Apple's new YAML, with every
  file claiming it came from the old commit, and `make verify` passing because it regenerated with
  the same stale commit and compared like with like. The three fields that describe the pin —
  `commit`, `yaml_sha256`, `os_versions` — could each be wrong indefinitely, and the file that
  exists to say what the tree came from was the one thing nothing checked.
- go-sdk-appleservices regenerates weekly and records nothing about the source, so a consumer
  cannot tell which Apple release a type belongs to.

## What they do

- **go-adm**, **go-sdk-appleservices**: generate from Apple's YAML and keep no provenance, so
  "which release is this?" is answered by reading the repository's git log, if at all.
- **NanoMDM**: hand-written, so the question does not arise, and neither does the answer.

## What we do better

1. The record is generated with everything else, so `make verify` holds it to the same
   determinism as the Go: it cannot drift from the tree it describes.
2. The commit is read from the checkout. `Options.Commit` remains for tests that generate from a
   synthetic tree, but every real caller leaves it empty and gets the truth.
3. Every field is a function of the checkout alone, which is why the commit's own committer date
   is recorded instead of the time the generator ran. A wall clock would make the file differ on
   every run, the determinism check would have to skip it, and it would be back to being
   unverified. `fetched` becomes `commit_date` for the same reason.
4. `os_versions` is computed as the highest version any schema in the tree is introduced in, and
   `admgen versions` reports it per OS family. That is what a schema update is worth reporting in
   terms of: a pin moving iOS from 26.0 to 26.4 says more than a commit hash does, and it is the
   line the update pipeline puts in the pull request.
5. `yaml_sha256` identifies the schema rather than the checkout: it hashes each YAML file's path
   and content in sorted order, so the same schema in a different directory hashes the same and a
   renamed file does not.

## Verified by

1. `schemagen.TestDescribeIsAFunctionOfTheCheckout`, and `make verify` green with
   `GENERATED_FROM.json` in the compared set.
2. Generating against Apple's previous release in a worktree produced its commit, its committer
   date of 2025-12-08, and `os_versions` 26.0, against 26.4 for the current pin — the old code
   would have stamped the pinned commit in both.
3. `schemagen.TestNewestIntroducedTakesTheHighestPerFamily` including Apple's `n/a`, which means
   a schema never shipped on that OS and must be absent rather than empty.
4. `schemagen.TestYAMLSHA256IdentifiesTheSchema` for content, path and location sensitivity;
   `TestCompareVersionsOrdersNumerically` for 26.10 above 26.9, which string ordering gets wrong.

## Rejected alternatives

- **Keep the file hand-maintained and add a lint**: the failure was not that someone forgot, but
  that nothing could tell. A generated file needs no discipline.
- **Regenerate it only when the commit changes**: keeps `fetched` meaningful and makes the
  generator stateful, with output depending on what is already on disk. Recording the commit's
  own date is simpler and true.
- **Drop `os_versions`**: it is the one field that says what an update *is* to a reader, rather
  than which bytes moved.
