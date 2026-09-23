# Lab acceptance stack

This Compose stack runs the reference server for local device acceptance. Use
[`dmctl lab`](../../docs/testing/lab.md) rather than calling Compose directly: it writes
the private container environment, publishes the ports the workspace declares, and waits
for readiness before running modules.

```sh
make lab-init LAB_MODE=live LAB_ADAPTER=docker LAB_HOSTS=mdm.lab.test,192.168.64.1
make lab-up
```

- **dmserver** is built from this checkout with the `runtime` target of the repository
  `Dockerfile`. Its state is the workspace's `mdm/` directory, bind-mounted at `/data`, so
  identities, the database and the push credential stay on the host.
- **fixtures** serves the workspace's `fixtures/` directory over HTTPS with the same lab
  identity, for files a device fetches: application packages and manifests, marker
  profiles and similar. It is read-only and serves `GET` and `HEAD` only.

Ports are published on `LAB_BIND` (loopback unless the workspace sets another address),
so nothing is exposed on the LAN by default. The container adapter runs live workspaces:
simulated Apple-service fixtures live in the lab process on the host, where a container
cannot reach them.

This is a local acceptance stack. It is not a deployment example; see
[the quick-start package](../quickstart/README.md) for that.
