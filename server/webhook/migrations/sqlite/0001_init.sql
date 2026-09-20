-- +up
CREATE TABLE webhook_subscriptions (
 id VARCHAR(64) PRIMARY KEY,
 revision INTEGER NOT NULL,
 config BLOB NOT NULL
);
CREATE TABLE webhook_messages (
 delivery_id VARCHAR(64) PRIMARY KEY,
 event_id VARCHAR(64) NOT NULL,
 subscription_id VARCHAR(64) NOT NULL,
 revision INTEGER NOT NULL,
 type VARCHAR(128) NOT NULL,
 occurred_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL,
 sensitive_capture INTEGER NOT NULL,
 payload BLOB,
 metadata TEXT NOT NULL
);
CREATE INDEX webhook_messages_event ON webhook_messages(event_id);
CREATE INDEX webhook_messages_subscription ON webhook_messages(subscription_id, revision);
CREATE INDEX webhook_messages_expiry ON webhook_messages(expires_at);
CREATE TABLE webhook_replays (
 id VARCHAR(64) PRIMARY KEY,
 request_hash VARCHAR(64) NOT NULL,
 result TEXT NOT NULL,
 created_at BIGINT NOT NULL
);
-- +down
DROP TABLE webhook_replays;
DROP TABLE webhook_messages;
DROP TABLE webhook_subscriptions;
