# Local reference-server Compose package

Start at the [guided walkthrough](../../docs/getting-started/reference-server.md).
It explains each command, expected output, JSON configuration and the additional
credentials needed for real enrollment.

From the repository root:

```sh
docker compose -f deploy/quickstart/compose.yaml --profile tools build
docker compose -f deploy/quickstart/compose.yaml up -d --wait
# Continue with the walkthrough to exchange the bootstrap secret and grant access.
```

`QUICKSTART_PORT` changes the loopback host port (default 8443). State lives in
the project's named `state` volume. `down` preserves it; `down -v` deletes it.
This is a single-host local composition. It does not provision public ingress,
Apple credentials or a device admission policy.

`bootstrap.py` delegates certificate/key operations to `dmctl setup`. It
serializes one-shot helpers, resumes interrupted first setup, and leaves
completed configuration and identities intact. `config` exports the JSON;
`apply-config` reads a complete document on stdin, validates local setup loading
and atomically publishes it. Storage relocation, key changes and identity-ID
changes require their dedicated operational workflows. Runtime settings need
a server restart after application.

Maintainers can run `make test-quickstart` for native bootstrap failure tests,
documentation examples and an isolated Compose smoke test. The smoke test uses
a unique project and ephemeral loopback port, verifies TLS, stored administrator
handoff and persistence, then removes only its own disposable volume. It does
not contact Apple or enroll a physical device.
