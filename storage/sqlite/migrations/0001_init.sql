-- +up
CREATE TABLE enrollments (
    id               TEXT      NOT NULL PRIMARY KEY,
    channel          INTEGER   NOT NULL,
    parent_id        TEXT      NOT NULL DEFAULT '',
    enabled          INTEGER   NOT NULL DEFAULT 0,
    topic            TEXT      NOT NULL DEFAULT '',
    push_magic       TEXT      NOT NULL DEFAULT '',
    push_token       BLOB      NULL,
    serial_number    TEXT      NOT NULL DEFAULT '',
    model            TEXT      NOT NULL DEFAULT '',
    model_name       TEXT      NOT NULL DEFAULT '',
    device_name      TEXT      NOT NULL DEFAULT '',
    product_name     TEXT      NOT NULL DEFAULT '',
    os_version       TEXT      NOT NULL DEFAULT '',
    build_version    TEXT      NOT NULL DEFAULT '',
    imei             TEXT      NOT NULL DEFAULT '',
    meid             TEXT      NOT NULL DEFAULT '',
    device_topic     TEXT      NOT NULL DEFAULT '',
    user_short_name  TEXT      NOT NULL DEFAULT '',
    user_long_name   TEXT      NOT NULL DEFAULT '',
    unlock_token     BLOB      NULL,
    authenticate_raw BLOB      NULL,
    token_update_raw BLOB      NULL,
    cert_hash        TEXT      NULL,
    bootstrap_token  BLOB      NULL,
    cert_hash_at     TIMESTAMP NULL,
    bootstrap_token_at TIMESTAMP NULL,
    enrolled_at      TIMESTAMP NOT NULL,
    token_updated_at TIMESTAMP NULL,
    last_seen_at     TIMESTAMP NOT NULL,
    disabled_at      TIMESTAMP NULL
);
CREATE INDEX idx_enrollments_parent ON enrollments (parent_id);
CREATE INDEX idx_enrollments_channel ON enrollments (channel, enabled);
CREATE INDEX idx_enrollments_serial ON enrollments (serial_number);
CREATE UNIQUE INDEX idx_enrollments_cert_hash ON enrollments (cert_hash) WHERE cert_hash IS NOT NULL;

CREATE TABLE commands (
    seq                INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id      TEXT      NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    command_uuid       TEXT      NOT NULL,
    request_type       TEXT      NOT NULL,
    raw                BLOB      NULL,
    dedupe_key         TEXT      NOT NULL DEFAULT '',
    state              TEXT      NOT NULL,
    enqueued_at        TIMESTAMP NOT NULL,
    last_sent_at       TIMESTAMP NULL,
    not_now_until      TIMESTAMP NULL,
    attempts           INTEGER   NOT NULL DEFAULT 0,
    not_now_count      INTEGER   NOT NULL DEFAULT 0,
    completed_at       TIMESTAMP NULL,
    result_status      TEXT      NULL,
    result_raw         BLOB      NULL,
    result_error_chain TEXT      NULL,
    UNIQUE (enrollment_id, command_uuid)
);
CREATE INDEX idx_commands_queue ON commands (enrollment_id, state, seq);
CREATE INDEX idx_commands_dedupe ON commands (enrollment_id, dedupe_key, state);

CREATE TABLE cert_associations (
    enrollment_id TEXT      NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    cert_hash     TEXT      NOT NULL,
    associated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, cert_hash)
);
CREATE INDEX idx_cert_associations_hash ON cert_associations (cert_hash, associated_at);

CREATE TABLE push_certs (
    topic      TEXT      NOT NULL PRIMARY KEY,
    cert_pem   BLOB      NOT NULL,
    key_pem    BLOB      NOT NULL,
    not_after  TIMESTAMP NOT NULL,
    version    INTEGER   NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE user_auth (
    enrollment_id    TEXT      NOT NULL PRIMARY KEY,
    parent_id        TEXT      NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    challenge        TEXT      NULL,
    challenge_at     TIMESTAMP NULL,
    auth_token       BLOB      NULL,
    token_at         TIMESTAMP NULL,
    authenticate_raw BLOB      NULL,
    digest_raw       BLOB      NULL,
    updated_at       TIMESTAMP NOT NULL
);
CREATE INDEX idx_user_auth_parent ON user_auth (parent_id);

-- +down
DROP TABLE user_auth;
DROP TABLE push_certs;
DROP TABLE cert_associations;
DROP TABLE commands;
DROP TABLE enrollments;
