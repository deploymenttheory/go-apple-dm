-- +up
CREATE TABLE enrollments (
    id               VARCHAR(255) NOT NULL PRIMARY KEY,
    channel          SMALLINT     NOT NULL,
    parent_id        VARCHAR(255) NOT NULL DEFAULT '',
    enabled          BOOLEAN      NOT NULL DEFAULT FALSE,
    topic            VARCHAR(255) NOT NULL DEFAULT '',
    push_magic       VARCHAR(255) NOT NULL DEFAULT '',
    push_token       BYTEA        NULL,
    serial_number    VARCHAR(255) NOT NULL DEFAULT '',
    model            VARCHAR(255) NOT NULL DEFAULT '',
    model_name       VARCHAR(255) NOT NULL DEFAULT '',
    device_name      TEXT         NOT NULL DEFAULT '',
    product_name     VARCHAR(255) NOT NULL DEFAULT '',
    os_version       VARCHAR(64)  NOT NULL DEFAULT '',
    build_version    VARCHAR(64)  NOT NULL DEFAULT '',
    imei             VARCHAR(64)  NOT NULL DEFAULT '',
    meid             VARCHAR(64)  NOT NULL DEFAULT '',
    device_topic     VARCHAR(255) NOT NULL DEFAULT '',
    user_short_name  VARCHAR(255) NOT NULL DEFAULT '',
    user_long_name   VARCHAR(255) NOT NULL DEFAULT '',
    unlock_token     BYTEA        NULL,
    authenticate_raw BYTEA        NULL,
    token_update_raw BYTEA        NULL,
    cert_hash        VARCHAR(128) NULL,
    bootstrap_token  BYTEA        NULL,
    cert_hash_at     TIMESTAMPTZ  NULL,
    bootstrap_token_at TIMESTAMPTZ NULL,
    enrolled_at      TIMESTAMPTZ  NOT NULL,
    token_updated_at TIMESTAMPTZ  NULL,
    last_seen_at     TIMESTAMPTZ  NOT NULL,
    disabled_at      TIMESTAMPTZ  NULL
);
CREATE INDEX idx_enrollments_parent ON enrollments (parent_id);
CREATE INDEX idx_enrollments_channel ON enrollments (channel, enabled);
CREATE INDEX idx_enrollments_serial ON enrollments (serial_number);
CREATE UNIQUE INDEX idx_enrollments_cert_hash ON enrollments (cert_hash) WHERE cert_hash IS NOT NULL;

CREATE TABLE commands (
    seq                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    enrollment_id      VARCHAR(255) NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    command_uuid       VARCHAR(255) NOT NULL,
    request_type       VARCHAR(255) NOT NULL,
    raw                BYTEA        NULL,
    dedupe_key         VARCHAR(255) NOT NULL DEFAULT '',
    state              VARCHAR(32)  NOT NULL,
    enqueued_at        TIMESTAMPTZ  NOT NULL,
    last_sent_at       TIMESTAMPTZ  NULL,
    not_now_until      TIMESTAMPTZ  NULL,
    attempts           INTEGER      NOT NULL DEFAULT 0,
    not_now_count      INTEGER      NOT NULL DEFAULT 0,
    completed_at       TIMESTAMPTZ  NULL,
    result_status      VARCHAR(32)  NULL,
    result_raw         BYTEA        NULL,
    result_error_chain TEXT         NULL,
    UNIQUE (enrollment_id, command_uuid)
);
CREATE INDEX idx_commands_queue ON commands (enrollment_id, state, seq);
CREATE INDEX idx_commands_dedupe ON commands (enrollment_id, dedupe_key, state);

CREATE TABLE cert_associations (
    enrollment_id VARCHAR(255) NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    cert_hash     VARCHAR(128) NOT NULL,
    associated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (enrollment_id, cert_hash)
);
CREATE INDEX idx_cert_associations_hash ON cert_associations (cert_hash, associated_at);

CREATE TABLE push_certs (
    topic      VARCHAR(255) NOT NULL PRIMARY KEY,
    cert_pem   BYTEA        NOT NULL,
    key_pem    BYTEA        NOT NULL,
    not_after  TIMESTAMPTZ  NOT NULL,
    version    BIGINT       NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ  NOT NULL
);

CREATE TABLE user_auth (
    enrollment_id    VARCHAR(255) NOT NULL PRIMARY KEY,
    parent_id        VARCHAR(255) NOT NULL REFERENCES enrollments (id) ON DELETE CASCADE,
    challenge        VARCHAR(512) NULL,
    challenge_at     TIMESTAMPTZ  NULL,
    auth_token       BYTEA        NULL,
    token_at         TIMESTAMPTZ  NULL,
    authenticate_raw BYTEA        NULL,
    digest_raw       BYTEA        NULL,
    updated_at       TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_user_auth_parent ON user_auth (parent_id);

-- +down
DROP TABLE user_auth;
DROP TABLE push_certs;
DROP TABLE cert_associations;
DROP TABLE commands;
DROP TABLE enrollments;
