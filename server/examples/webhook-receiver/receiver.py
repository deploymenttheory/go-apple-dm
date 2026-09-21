"""Standard Webhooks receiver with a persisted, deduplicated SQLite inbox.

Run behind verified HTTPS. This example stores received bodies; protect its
directory and database according to the subscription's disclosure policy.
"""

import base64
import hmac
import json
import os
import sqlite3
import time
from http.server import BaseHTTPRequestHandler, HTTPServer


def verify(secret, headers, body, now=None):
    """Authenticate exact bytes before JSON decoding; reject stale deliveries."""
    now = time.time() if now is None else now
    try:
        key = base64.b64decode(secret.removeprefix("whsec_"), validate=True)
        delivery_id = headers["webhook-id"]
        timestamp = headers["webhook-timestamp"]
        if len(key) != 32 or not delivery_id or len(delivery_id) > 64:
            return False
        if abs(now - int(timestamp)) > 300:
            return False
        message = delivery_id.encode() + b"." + timestamp.encode() + b"." + body
        signature = base64.b64encode(hmac.digest(key, message, "sha256")).decode()
        return any(
            hmac.compare_digest(candidate, "v1," + signature)
            for candidate in headers["webhook-signature"].split()
        )
    except (ValueError, KeyError, TypeError, AttributeError):
        return False


def main():
    secret = os.environ["WEBHOOK_SIGNING_SECRET"]
    os.umask(0o077)
    inbox = sqlite3.connect(os.environ.get("WEBHOOK_INBOX", "webhooks.sqlite"))
    inbox.execute("PRAGMA journal_mode=WAL")
    inbox.execute("PRAGMA synchronous=FULL")
    inbox.execute(
        "CREATE TABLE IF NOT EXISTS inbox "
        "(delivery_id TEXT PRIMARY KEY, event_id TEXT NOT NULL, body BLOB NOT NULL)"
    )
    inbox.commit()

    class Receiver(BaseHTTPRequestHandler):
        def do_POST(self):
            if self.path != "/webhook":
                self.send_error(404)
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                length = 0
            if not 0 < length <= 256 * 1024:
                self.send_error(413)
                return
            body = self.rfile.read(length)
            if len(body) != length:
                self.send_error(400)
                return
            names = ("webhook-id", "webhook-timestamp", "webhook-signature")
            if any(len(self.headers.get_all(name, [])) != 1 for name in names):
                self.send_error(401)
                return
            if not verify(secret, self.headers, body):
                self.send_error(401)
                return
            try:
                event = json.loads(body)
                if event["schema_version"] != "1" or not isinstance(event["event_id"], str):
                    raise ValueError("unsupported event")
            except (ValueError, KeyError, TypeError):
                self.send_error(400)
                return
            try:
                with inbox:
                    inbox.execute(
                        "INSERT OR IGNORE INTO inbox (delivery_id,event_id,body) VALUES (?,?,?)",
                        (self.headers["webhook-id"], event["event_id"], body),
                    )
            except sqlite3.Error:
                self.send_error(503)
                return
            # A separate workflow consumer processes this persisted inbox.
            self.send_response(204)
            self.end_headers()

        def log_message(self, *_):
            pass  # Keep protocol bodies and credentials out of request logs.

    try:
        HTTPServer(("127.0.0.1", int(os.environ.get("PORT", "8080"))), Receiver).serve_forever()
    finally:
        inbox.close()


if __name__ == "__main__":
    main()
