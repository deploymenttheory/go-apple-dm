# Server backup and recovery

`dmctl recovery` preserves the reference server's SQL state, setup document,
referenced files, and original storage keys in an authenticated age archive.
SQLite, PostgreSQL, and MySQL use the same workflow. Restore requires the same
backend and compiled schema; this is not a schema upgrade or backend conversion.

Keep recovery material outside source control. For the local Mac lab, use
`test-lab/local/certs` for archives, identity files, maintenance tickets, staging,
and restored directories. Copy the encrypted archive and its separately held
recovery identity to the intended recovery host before claiming off-machine recovery.

## Prepare

Use a setup document containing the deployment's complete configuration. Run
backup with the same `DM_` environment overrides as the source processes. All
processes sharing a database must run a server version with maintenance support.
Embedding applications must register their writers with `server/maintenance` and
stop all writes before acknowledging a fence. Unregistered SQL writers and external
configuration editors must be stopped for the whole checkpoint operation.

File references and every active or accepted storage key are copied. Original
key IDs are retained; changing a key's name changes its cryptographic binding.
External secret providers must export their original named keys to the configured
key directory before backup. Unknown application tables cause backup to fail;
embedding applications register their compiled migration sets through `recovery.SQL`.

Generate a recovery identity once:

```sh
dmctl recovery keygen \
  -identity-file test-lab/local/certs/recovery.agekey
```

The command prints only the public recipient. Retain the private identity
separately from archives. Existing identities and archives are never overwritten.

## Pause and back up

```sh
dmctl recovery pause \
  -setup-file test-lab/local/certs/managed/setup.json \
  -ticket-file test-lab/local/certs/backup-ticket

dmctl recovery backup \
  -setup-file test-lab/local/certs/managed/setup.json \
  -ticket-file test-lab/local/certs/backup-ticket \
  -archive test-lab/local/certs/server-checkpoint.age \
  -recipient age1REPLACE_WITH_PUBLIC_RECIPIENT \
  -revision REPLACE_WITH_SOURCE_COMMIT \
  -staging-dir test-lab/local/certs
```

Pause is persistent. It rejects new server/setup registrations and HTTP requests,
cancels background workers, and waits for admitted requests and workers to finish
on every registered process. A timeout leaves the fence closed and reports it.
The ticket is written before the fence is requested, allowing recovery after a
CLI crash. Do not run a concurrent resume while a checkpoint is being made.

Inspect a stalled drain with `recovery status -setup-file PATH`. Participants
never expire automatically. After independently stopping a crashed or unreachable
process, remove only its registration:

```sh
dmctl recovery forget -setup-file PATH -ticket-file TICKET \
  -member PARTICIPANT_ID -process-stopped
```

Forgetting a process that can still write invalidates the consistency guarantee.

## Verify and resume the source

```sh
dmctl recovery verify \
  -archive test-lab/local/certs/server-checkpoint.age \
  -identity-file test-lab/local/certs/recovery.agekey \
  -staging-dir test-lab/local/certs

dmctl recovery resume \
  -setup-file test-lab/local/certs/managed/setup.json \
  -ticket-file test-lab/local/certs/backup-ticket
```

Verification authenticates the entire ciphertext, manifest and file hashes,
checks every encrypted SQL value with its column and row binding, then exercises
schema, row, constraint and sequence restoration in an isolated database.
Sequence values must be at least as high as every restored row, while retaining
higher values left by pruned rows. A checkpoint that would reuse a cursor is refused
before restored rows commit.
SQLite uses a private temporary file. PostgreSQL/MySQL require
`-verify-dsn-env RECOVERY_VERIFY_DSN` pointing to a dedicated empty database/schema.
That verification database remains an isolated, paused copy; remove it through
your database administration process after inspection. No listener or worker starts.

## Restore

Stop every source server process before activating a restored deployment. Preserve
the existing HTTPS URL, APNs topic and identity; do not re-enroll managed devices.

```sh
dmctl recovery restore \
  -archive test-lab/local/certs/server-checkpoint.age \
  -identity-file test-lab/local/certs/recovery.agekey \
  -target-dir test-lab/local/certs/restored \
  -staging-dir test-lab/local/certs \
  -source-stopped
```

SQLite creates its database inside the absent target directory. PostgreSQL/MySQL
also require `-target-dsn-env RECOVERY_TARGET_DSN` naming an empty database/schema.
An occupied directory or database is refused. A failed restore can leave a partial
isolated target, particularly after MySQL DDL; inspect it and choose a fresh empty
target for another attempt. A runnable setup document is written only after all
restore checks succeed.

The result names the restored setup and maintenance-ticket files. The restored
deployment remains paused. Resume it explicitly, then start the server with the
returned setup document:

```sh
dmctl recovery resume -setup-file RESTORED_SETUP -ticket-file RESTORED_TICKET
dmserver --setup-file RESTORED_SETUP
```

Run `setup check`, a fresh device `DeviceInformation` request, the installing-user
`ProfileList`, and the harmless DDM subscription scenario. Verify fresh responses,
the served HTTPS certificate, enrollment certificate pins and TokenUpdate times.
Archive validity alone does not prove network reachability or device continuity.

The public Go APIs are `recovery.SQL.Backup`, `recovery.Prepare`,
`Prepared.CheckDatabase`, `Prepared.Restore`, and `maintenance.Store`.
Archives contain all command and event-delivery state. Webhook delivery remains
at least once; receivers use the stable event ID to deduplicate retries.
