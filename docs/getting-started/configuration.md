# Configuration: server, CLI and lab

[Getting started](getting-started.md) · [Reference-server walkthrough](reference-server.md)

There are three different JSON files. Pick the one for the program you are configuring:

| File | Read by | Purpose |
|---|---|---|
| `setup.json` | `dmserver -setup-file …`, `DM_SETUP_FILE`, or local `dmctl setup … -setup-file …` | Server settings, secret references and managed certificate identities |
| `dmctl.json` | `dmctl -config …` or `DMCTL_CONFIG` | Administrative client contexts and credential references |
| `lab.json` | `dmctl lab … -workspace …` | A testing workspace with fixtures and supervised processes |

The reference server also accepts `DM_*` environment variables without a setup
file. The Compose walkthrough uses `setup.json` so you can inspect and edit one
document. Custom servers choose their own configuration format; importing the
libraries does not make them read any of these files.

## 1. Create server configuration

The [Compose quickstart](reference-server.md#1-start-the-compose-package) generates
the file and its keys automatically. Export it to a protected local file:

```sh
umask 077
mkdir -p test-lab/local/quickstart
docker compose -f deploy/quickstart/compose.yaml run --rm -T bootstrap config \
  > test-lab/local/quickstart/setup.json
```

For a native installation, generate its own configuration instead:

```sh
./bin/dmctl setup init -dir test-lab/local/native -role customer \
  -public-url https://localhost:8443 -listen 127.0.0.1:8443
```

This writes protected secret files and initializes the database. It does not
create all the certificates or start the server. Continue with
[native startup](reference-server.md#7-run-without-docker).

Here is the complete generated **Compose** document, with ordering normalized:

```json
{
  "version": 1,
  "environment": {
    "DM_LISTEN": "0.0.0.0:8443",
    "DM_PUBLIC_URL": "https://localhost:8443",
    "DM_ORGANIZATION": "go-apple-dm",
    "DM_STORAGE": "sqlite",
    "DM_DSN": "/data/certificates.sqlite",
    "DM_STORAGE_KEYS": "storage",
    "DM_SECRETS_DIR": "/data/secrets",
    "DM_STORAGE_KEYS_STRICT": "true",
    "DM_IDENTITY": "acme",
    "DM_AUDIT_STORE": "true",
    "DM_AUDIT_RETENTION": "720h"
  },
  "secretFiles": {
    "DM_BOOTSTRAP_TOKEN": "/data/secrets/admin",
    "DM_SCEP_HMAC_KEY": "/data/secrets/issuance"
  },
  "setup": {
    "role": "customer",
    "vendorId": "vendor",
    "pushId": "push",
    "httpsId": "https",
    "issuerId": "issuer",
    "httpsCaId": "https-ca"
  }
}
```

All `/data` paths here are **inside the container**. Copying this example onto
your host does not create its referenced keys. Generate native configuration
with `setup init` to get the correct absolute host paths.

## 2. Understand the document

`version` must be the JSON number `1`. `environment` maps recognized `DM_*`
names to **strings**, including booleans and durations: use `"true"`, not `true`.
`secretFiles` maps the same names to files containing their values. `setup`
selects the managed certificate workflow and its identity IDs.

The server always runs unified device management. The certificate workflow has its own role:

| Setting | Choices | Meaning |
|---|---|---|
| `setup.role` | `customer`, `vendor`, `combined` | Whether it manages customer identities, signs customer push requests, or does both |

The `httpsId`, `issuerId`, `pushId` and `vendorId` settings name persistent certificate
workflows in the database, not file paths. They default to the identity they hold:
`server-https`, `enrollment-ca`, `mdm-push` and `vendor-signing`. `httpsCaId` names the
local HTTPS CA and defaults to `server-https-ca`. `setup.http01Listen` enables the
public ACME challenge listener; `setup.vendorUrl` and `setup.vendorTokenFile`
configure a remote vendor signing service. See [certificate setup](../operations/certificate-lifecycle.md).

### Precedence and paths

For each `DM_*` name, the setup loader uses the first **nonempty** value:

1. The process environment.
2. The trimmed content of the named `secretFiles` file.
3. The value in `environment`.
4. The setting's default in the server parser.

A nonempty process override skips reading that name's secret file. An empty
environment variable does **not** disable a value from JSON. To remove the
bootstrap admin token, remove its JSON reference and any inline value, then
unset the process variable and restart.

`dmserver` flags such as `-listen`, `-storage` and `-dsn` then override their
loaded configuration fields for that invocation. Local `dmctl setup` operations
do not inherit those server flags. Prefer one setup file and explicit process
overrides so startup and diagnostic commands inspect the same configuration.

Relative `secretFiles` paths and `setup.vendorTokenFile` are resolved relative
to `setup.json`. Other paths, including `DM_DSN`, `DM_SECRETS_DIR` and
`DM_ENROLLMENT_POLICY_FILE`, are interpreted by their consumers relative to the
process working directory. Prefer absolute paths for those settings.

There is no `${NAME}`, `$HOME` or `~` expansion inside JSON. Compose's YAML
interpolation is a separate feature; it does not interpolate `setup.json`.
Unknown setup fields and unrecognized `DM_*` names are not a reliable typo
check: the loader can ignore them. Match the spelling in the tables below or
the [environment parser](../../server/internal/app/env.go).

### Storage keys and secret files

`DM_STORAGE_KEYS` is a comma-separated list of key **names**, active first.
With `DM_SECRETS_DIR=/data/secrets`, key `storage` is read from
`/data/secrets/storage`. This directory is a storage-key provider; it does not
automatically load every file as a `DM_*` setting. Without the directory
provider, material comes from `DM_STORAGE_KEY_<NAME>`.

The generated random hexadecimal text is consumed as bytes, then derived with
HKDF; do not decode it when copying or restoring it. Retain both the name and
the bytes. SQL stores seal designated sensitive values using AES-256-GCM;
this is not whole-database encryption. Raw messages, public metadata and exports
can still contain sensitive data. Preserve the keys separately from database
backups, and follow the [recovery guide](../operations/recovery.md).

`setup init` writes secrets and configuration with mode 0600 and creates its
secret directory with mode 0700. Keep edited files equally protected and owned
by the account that reads them. The general setup loader does not impose a
blanket permission check on every referenced file; protect files at deployment.
Use equivalent access controls on Windows.

## 3. Change a setting and apply it

Open the exported `test-lab/local/quickstart/setup.json` in an editor. For a
first edit, change `DM_ORGANIZATION` to your lab name. Keep the other values.
Apply the **whole document**, then restart:

```sh
docker compose -f deploy/quickstart/compose.yaml run --rm -T bootstrap apply-config \
  < test-lab/local/quickstart/setup.json
docker compose -f deploy/quickstart/compose.yaml restart dmserver
docker compose -f deploy/quickstart/compose.yaml run --rm -T dmctl status
```

The helper prints:

```text
Configuration saved. Restart dmserver to apply it.
```

It checks JSON types, secret references and local setup loading before replacing
the file atomically. It refuses storage/key-location and certificate-ID changes;
those need the recovery or key-rotation workflow. Failed validation retains the
previous file. This is **not a full deployment validation**: it cannot prove
public DNS, Apple credentials, device trust, or that all runtime services will
start. Inspect `docker compose … logs dmserver` and rerun `status` after restart.

For a native installation, edit its generated file and run:

```sh
./bin/dmctl setup status -setup-file test-lab/local/native/setup.json
./bin/dmctl setup check -setup-file test-lab/local/native/setup.json
```

`status` reports certificate state without starting listeners. `check` returns
exit code 3 while required identities are incomplete. Once they are ready, the
local check also builds the complete configuration and checks its TLS identity.
Local `status` does not run enrollment services, so use remote `setup status`
to check the running process's `enrollmentEnabled` field. Server configuration
edits require restart; managed certificate transitions have their own reload
behavior described in the [lifecycle guide](../operations/certificate-lifecycle.md).

## 4. Settings you will change first

These are parser defaults **before** managed setup and the quickstart apply
their choices. Additional settings are defined in
[env.go](../../server/internal/app/env.go) and its
[security settings parser](../../server/internal/app/securityenv.go).

| Setting | Default / effect | When to set it |
|---|---|---|
| `DM_LISTEN` | `127.0.0.1:8080` | Listener address; non-loopback requires TLS |
| `DM_PUBLIC_URL` | Unset | Stable device-facing HTTPS base URL, including a nonstandard port |
| `DM_ORGANIZATION` | `go-apple-dm` in generated setup | Organization shown in enrollment material |
| `DM_STORAGE`, `DM_DSN` | `sqlite`, `dm.db` | Select storage; managed setup writes absolute SQLite paths |
| `DM_STORAGE_KEYS`, `DM_SECRETS_DIR` | Unset | Required stable keyring for persistent reference-server storage |
| `DM_STORAGE_KEYS_STRICT` | Enabled by setup init | Reject legacy plaintext in encrypted fields |
| `DM_BOOTSTRAP_TOKEN` | Unset | One-time creation of the first stored root; accepted only by the bootstrap endpoint |
| `DM_AUDIT_STORE` | `false`; quickstart sets `true` | Persist projected audit events |
| `DM_AUDIT_RETENTION` | Quickstart sets `720h` | Audit retention interval |
| `DM_ENROLLMENT_POLICY_FILE` | Unset; admission denies without a matching policy | JSON rules for admitted devices/accounts |
| `DM_IDENTITY` | `scep` in parser; setup init selects `acme` | Enrollment identity method |
| `DM_ACME_ALLOW_UNATTESTED` | `false` | Explicit policy for software-key ACME; does not configure trust |
| `DM_DDM_SUBSCRIPTIONS` | `true` | Automatic status subscription declarations |
| `DM_ALLOW_REENROLL`, `DM_RETURN_TO_SERVICE` | `false` | Deliberate reenrollment/erasure policies; leave off for initial setup |

Managed setup loads HTTPS, push and issuer material from encrypted storage.
Do not combine it with `DM_TLS_CERT_FILE`/`DM_TLS_KEY_FILE`, static enrollment
CA files, or a file-based push identity. Those are the alternative unmanaged
configuration path, not extra prerequisites for Compose.

PostgreSQL/MySQL need a provisioned database and a protected DSN. Pass `-storage`
and `-dsn-env` to `setup init`; a credential-bearing DSN is written to a secret
file. Changing a DSN does not migrate existing state. The reference server uses one database for unified device management.

## 5. Configure a native administrative CLI

The Compose `dmctl` service already supplies its server, token reference and CA.
For a native CLI, create a context using an absolute token-file path:

```json
{
  "current": "lab",
  "contexts": {
    "lab": {
      "server": "https://localhost:8443",
      "token_file": "/absolute/path/to/operator-token"
    }
  }
}
```

Save it as `test-lab/local/dmctl.json`, protect it and supply HTTPS trust:

```sh
chmod 600 test-lab/local/dmctl.json
./bin/dmctl -config test-lab/local/dmctl.json \
  -ca-file /absolute/path/to/https-ca.pem status
```

The default path is `DMCTL_CONFIG`, otherwise
`$XDG_CONFIG_HOME/go-apple-dm/dmctl.json`, or
`$HOME/.config/go-apple-dm/dmctl.json`. On Unix, the CLI refuses a configuration
file readable by other users. It does not use the server's `setup.json` as an
administrative client context.

Explicit CLI flags take precedence over their `DMCTL_*` environment defaults.
`-context`/`DMCTL_CONTEXT` select a context before `current`. Explicit
`-server`/`DMCTL_SERVER` override its server. Explicit `-token`/`DMCTL_TOKEN`
override context credentials and accept a literal, `@/path/to/file`, or
`env:VARIABLE_NAME`. Within a context, resolution is `token_env`, then
`token_file`, then inline `token`. An empty selected environment credential
fails; it does not fall back to another source.

Use `token_env` or `token_file` instead of committing a token. Relative
`token_file` paths are relative to the CLI's working directory, not to the
configuration file. There is no shell expansion. A CA path is supplied with
`-ca-file` or `DMCTL_CA_FILE`; it is not a context JSON field. TLS verification
cannot be disabled with `-insecure`.

## 6. Keep lab configuration separate

`dmctl lab init` writes `lab.json`, secrets and fixture state in the selected
workspace. Use `dmctl lab doctor -workspace …` to check it. It is not accepted
by `dmserver -setup-file` or `dmctl -config`. Do not reuse a simulated workspace
for live devices; its trust anchors and Apple-service fixtures are test material.

The document records `Mode` (`simulated` or `live`), `Storage`, `Listen`, the
server `Adapter` (`process` or `docker`), the device-facing `Hosts` in the HTTPS
leaf, and the `Bind` address the container adapter publishes on. A workspace
written before the lab replaced the bench is still read from its `bench.json`.

Follow the [lab guide](../testing/lab.md) and
[lab configuration reference](../operations/reference-lab.md) for modes,
topologies and evidence. `setup adopt -from-lab` is an explicit migration
workflow for a compatible live workspace, not a rename of the workspace document.
