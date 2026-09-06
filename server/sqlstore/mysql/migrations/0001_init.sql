-- +up
CREATE TABLE enrollments (
    id               VARCHAR(255) NOT NULL PRIMARY KEY,
    channel          SMALLINT     NOT NULL,
    parent_id        VARCHAR(255) NOT NULL DEFAULT '',
    enabled          BOOLEAN      NOT NULL DEFAULT FALSE,
    topic            VARCHAR(255) NOT NULL DEFAULT '',
    push_magic       VARCHAR(255) NOT NULL DEFAULT '',
    push_token       VARBINARY(1024) NULL,
    serial_number    VARCHAR(255) NOT NULL DEFAULT '',
    model            VARCHAR(255) NOT NULL DEFAULT '',
    model_name       VARCHAR(255) NOT NULL DEFAULT '',
    device_name      TEXT         NOT NULL,
    product_name     VARCHAR(255) NOT NULL DEFAULT '',
    os_version       VARCHAR(64)  NOT NULL DEFAULT '',
    build_version    VARCHAR(64)  NOT NULL DEFAULT '',
    imei             VARCHAR(64)  NOT NULL DEFAULT '',
    meid             VARCHAR(64)  NOT NULL DEFAULT '',
    device_topic     VARCHAR(255) NOT NULL DEFAULT '',
    user_short_name  VARCHAR(255) NOT NULL DEFAULT '',
    user_long_name   VARCHAR(255) NOT NULL DEFAULT '',
    not_on_console   BOOLEAN NOT NULL DEFAULT FALSE,
    enrollment_user_id VARCHAR(255) NOT NULL DEFAULT '',
    unlock_token     MEDIUMBLOB   NULL,
    authenticate_raw MEDIUMBLOB   NULL,
    token_update_raw MEDIUMBLOB   NULL,
    cert_hash        VARCHAR(128) NULL,
    bootstrap_token  BLOB         NULL,
    cert_hash_at     DATETIME(6)  NULL,
    bootstrap_token_at DATETIME(6) NULL,
    enrolled_at      DATETIME(6)  NOT NULL,
    token_updated_at DATETIME(6)  NULL,
    last_seen_at     DATETIME(6)  NOT NULL,
    disabled_at      DATETIME(6)  NULL,
    INDEX idx_enrollments_parent (parent_id),
    INDEX idx_enrollments_channel (channel, enabled),
    INDEX idx_enrollments_serial (serial_number),
    UNIQUE INDEX idx_enrollments_cert_hash (cert_hash)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE commands (
    seq                BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    enrollment_id      VARCHAR(255) NOT NULL,
    command_uuid       VARCHAR(255) NOT NULL,
    request_type       VARCHAR(255) NOT NULL,
    raw                MEDIUMBLOB   NULL,
    dedupe_key         VARCHAR(255) NOT NULL DEFAULT '',
    state              VARCHAR(32)  NOT NULL,
    enqueued_at        DATETIME(6)  NOT NULL,
    last_sent_at       DATETIME(6)  NULL,
    not_now_until      DATETIME(6)  NULL,
    attempts           INTEGER      NOT NULL DEFAULT 0,
    not_now_count      INTEGER      NOT NULL DEFAULT 0,
    completed_at       DATETIME(6)  NULL,
    result_status      VARCHAR(32)  NULL,
    result_raw         MEDIUMBLOB   NULL,
    result_error_chain TEXT         NULL,
    UNIQUE INDEX idx_commands_uuid (enrollment_id, command_uuid),
    INDEX idx_commands_queue (enrollment_id, state, seq),
    INDEX idx_commands_dedupe (enrollment_id, dedupe_key, state),
    CONSTRAINT fk_commands_enrollment FOREIGN KEY (enrollment_id) REFERENCES enrollments (id) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE cert_associations (
    enrollment_id VARCHAR(255) NOT NULL,
    cert_hash     VARCHAR(128) NOT NULL,
    associated_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (enrollment_id, cert_hash),
    INDEX idx_cert_associations_hash (cert_hash, associated_at),
    CONSTRAINT fk_cert_assoc_enrollment FOREIGN KEY (enrollment_id) REFERENCES enrollments (id) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE push_certs (
    topic      VARCHAR(255) NOT NULL PRIMARY KEY,
    cert_pem   BLOB         NOT NULL,
    key_pem    BLOB         NOT NULL,
    not_after  DATETIME(6)  NOT NULL,
    version    BIGINT       NOT NULL DEFAULT 1,
    updated_at DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE user_auth (
    enrollment_id    VARCHAR(255) NOT NULL PRIMARY KEY,
    parent_id        VARCHAR(255) NOT NULL,
    challenge        VARCHAR(512) NULL,
    challenge_at     DATETIME(6)  NULL,
    auth_token       BLOB         NULL,
    token_at         DATETIME(6)  NULL,
    authenticate_raw MEDIUMBLOB   NULL,
    digest_raw       MEDIUMBLOB   NULL,
    updated_at       DATETIME(6)  NOT NULL,
    INDEX idx_user_auth_parent (parent_id),
    CONSTRAINT fk_user_auth_parent FOREIGN KEY (parent_id) REFERENCES enrollments (id) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE user_auth;
DROP TABLE push_certs;
DROP TABLE cert_associations;
DROP TABLE commands;
DROP TABLE enrollments;
