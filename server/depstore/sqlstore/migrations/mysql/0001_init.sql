-- +up
CREATE TABLE dep_accounts (
    name                VARCHAR(255) NOT NULL PRIMARY KEY,
    consumer_key        VARCHAR(255) NOT NULL DEFAULT '',
    consumer_secret     BLOB         NULL,
    access_token        BLOB         NULL,
    access_secret       BLOB         NULL,
    access_token_expiry DATETIME(6)  NULL,
    protocol_version    INT          NOT NULL DEFAULT 0,
    org_name            TEXT         NOT NULL,
    org_id              VARCHAR(255) NOT NULL DEFAULT '',
    server_name         TEXT         NOT NULL,
    server_uuid         VARCHAR(255) NOT NULL DEFAULT '',
    admin_id            TEXT         NOT NULL,
    limits              BLOB         NULL,
    profile_uuid        VARCHAR(255) NOT NULL DEFAULT '',
    terms_expired       BOOLEAN      NOT NULL DEFAULT FALSE,
    token_invalid       BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at          DATETIME(6)  NOT NULL,
    updated_at          DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_sessions (
    account    VARCHAR(255) NOT NULL PRIMARY KEY,
    token      BLOB         NOT NULL,
    updated_at DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_cursors (
    account       VARCHAR(255) NOT NULL PRIMARY KEY,
    value         TEXT         NOT NULL,
    phase         VARCHAR(16)  NOT NULL,
    fetched_until DATETIME(6)  NULL,
    updated_at    DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_keypairs (
    account    VARCHAR(255) NOT NULL,
    stage      VARCHAR(16)  NOT NULL,
    cert_pem   BLOB         NOT NULL,
    key_pem    BLOB         NOT NULL,
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (account, stage)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_devices (
    account        VARCHAR(255) NOT NULL,
    serial_number  VARCHAR(255) NOT NULL,
    record         MEDIUMBLOB   NOT NULL,
    profile_uuid   VARCHAR(255) NOT NULL DEFAULT '',
    profile_status VARCHAR(32)  NOT NULL DEFAULT '',
    op_type        VARCHAR(32)  NOT NULL DEFAULT '',
    op_date        DATETIME(6)  NULL,
    deleted        BOOLEAN      NOT NULL DEFAULT FALSE,
    first_seen     DATETIME(6)  NOT NULL,
    updated_at     DATETIME(6)  NOT NULL,
    PRIMARY KEY (account, serial_number),
    INDEX idx_dep_devices_profile (account, profile_uuid, serial_number)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_profiles (
    account      VARCHAR(255) NOT NULL,
    profile_uuid VARCHAR(255) NOT NULL,
    record       MEDIUMBLOB   NOT NULL,
    updated_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (account, profile_uuid)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE dep_assignments (
    account         VARCHAR(255) NOT NULL,
    serial_number   VARCHAR(255) NOT NULL,
    profile_uuid    VARCHAR(255) NOT NULL DEFAULT '',
    status          VARCHAR(32)  NOT NULL,
    attempts        INT          NOT NULL DEFAULT 0,
    last_error      TEXT         NOT NULL,
    attempted_at    DATETIME(6)  NULL,
    next_attempt_at DATETIME(6)  NULL,
    PRIMARY KEY (account, serial_number),
    INDEX idx_dep_assignments_status (account, status, serial_number)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE dep_assignments;
DROP TABLE dep_profiles;
DROP TABLE dep_devices;
DROP TABLE dep_keypairs;
DROP TABLE dep_cursors;
DROP TABLE dep_sessions;
DROP TABLE dep_accounts;
