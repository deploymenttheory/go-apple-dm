# Getting started with go-apple-dm

Choose the path that matches what you want to build:

| Your goal | Start here | First result |
|---|---|---|
| Run the supplied server and learn its workflows | [Reference server](reference-server.md) | A persistent HTTPS server, working CLI and a guided route to your first enrolled device |
| Build a server around the Go libraries | [Build your own Apple DM server](build-your-own-apple-dm-server.md) | A typed command, a small service composition and a clear list of the product behavior you must implement |

The reference-server path includes a ready-to-run Docker Compose package. You
need Git and Docker Compose; Go and Apple credentials are unnecessary for the
initial local startup. The custom-server path requires Go and familiarity with
HTTP services, persistence and certificate-based authentication.

[Configuration explained](configuration.md) shows how to write and apply
`setup.json`, how the administrative CLI's `dmctl.json` works, and why a
`lab.json` testing workspace is separate.

## What is in this project?

- **Go libraries**: Apple protocol types, validation, enrollment, PKI, Apple
  service clients and storage contracts.
- **Reference server (`dmserver`)**: a runnable composition of those components,
  with persistence, administrative APIs and background workers.
- **Administrative CLI (`dmctl`)**: inspect and operate the server, prepare
  certificates, inspect schema support offline, and run the testing lab.

There is no fleet-management web UI or administrator username/password login.
Administration uses tokens and, when enabled, stored principals and Cedar policies.

The project is pre-1.0. Use documentation and binaries from the same revision.
A healthy local server proves startup and connectivity; enrolling a real device
also requires trusted HTTPS, Apple MDM push credentials, admission policy and
an authorized device. Simulator results do not establish compatibility with
every OS, hardware model or enrollment mode.

After your first walkthrough, use the [documentation index](../README.md) for
certificate renewal, backups, enrollment security, DDM and testing procedures.
