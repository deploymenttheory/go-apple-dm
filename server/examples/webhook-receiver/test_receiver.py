import base64
import hashlib
import hmac
import unittest

from receiver import verify


class VerificationTests(unittest.TestCase):
    def test_standard_webhooks(self):
        key = b"0123456789abcdef0123456789abcdef"
        secret = "whsec_" + base64.b64encode(key).decode()
        body = b'{"type":"webhook.test"}'
        signature = base64.b64encode(
            hmac.new(key, b"delivery-example.1800446400." + body, hashlib.sha256).digest()
        ).decode()
        headers = {
            "webhook-id": "delivery-example",
            "webhook-timestamp": "1800446400",
            "webhook-signature": "v1,oldkey v1," + signature,
        }
        self.assertTrue(verify(secret, headers, body, 1800446400))
        self.assertFalse(verify(secret, headers, body + b" ", 1800446400))
        self.assertFalse(verify(secret, headers, body, 1800446800))
        self.assertFalse(verify("invalid", headers, body, 1800446400))
        self.assertFalse(verify(secret, {}, body, 1800446400))


if __name__ == "__main__":
    unittest.main()
