# Python receiver

This standard-library example verifies the exact POST body with Standard Webhooks
headers, rejects stale signatures, and commits an inbox record before acknowledging.
The delivery ID is a SQLite primary key, so retries cannot enqueue the same delivery
twice. A separate workflow consumer can process this inbox. Explicit replay has a
new delivery ID and therefore creates another inbox record.

```sh
export WEBHOOK_SIGNING_SECRET='whsec_REPLACE_WITH_CREATION_OR_ROTATION_SECRET'
export WEBHOOK_INBOX='/private/application-directory/webhooks.sqlite'
python3 receiver.py
```

Run behind an HTTPS reverse proxy forwarding `/webhook` to `127.0.0.1:8080`. The
server sends only to verified HTTPS URLs. For a private test receiver, configure
the server's trust roots and allowed private CIDRs. The example does not terminate
TLS or implement a production workflow worker. It deliberately does not print bodies.
The inbox contains plaintext received bodies; use a protected directory, storage
encryption and retention appropriate for the subscription.

Payload references require the separate subscription-scoped `payload_token` from
subscription creation or credential rotation. Fetch relative references only from
the configured MDM server origin, sending `Authorization: Bearer PAYLOAD_TOKEN`.
Verify the returned body length and SHA-256 against the payload descriptor. Do not
send administrator credentials or follow references to arbitrary hosts.

```sh
python3 -m unittest discover -s server/examples/webhook-receiver
```

See [native webhooks](../../../docs/operations/webhooks.md) and the
[JSON fixtures](../../webhook/testdata).
