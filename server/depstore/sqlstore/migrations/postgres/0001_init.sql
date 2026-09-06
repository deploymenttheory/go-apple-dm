-- +up
CREATE TABLE dep_accounts (
    name                VARCHAR(255) NOT NULL PRIMARY KEY,
    consumer_key        VARCHAR(255) NOT NULL DEFAULT '',
    consumer_secret     BYTEA        NULL,
    access_token        BYTEA        NULL,
    access_secret       BYTEA        NULL,
    access_token_expiry TIMESTAMPTZ  NULL,
    protocol_version    INTEGER      NOT NULL DEFAULT 0,
    org_name            TEXT         NOT NULL DEFAULT '',
    org_id              VARCHAR(255) NOT NULL DEFAULT '',
    server_name         TEXT         NOT NULL DEFAULT '',
    server_uuid         VARCHAR(255) NOT NULL DEFAULT '',
    admin_id            TEXT         NOT NULL DEFAULT '',
    limits              BYTEA        NULL,
    profile_uuid        VARCHAR(255) NOT NULL DEFAULT '',
    terms_expired       BOOLEAN      NOT NULL DEFAULT FALSE,
    token_invalid       BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ  NOT NULL,
    updated_at          TIMESTAMPTZ  NOT NULL
);

CREATE TABLE dep_sessions (
    account    VARCHAR(255) NOT NULL PRIMARY KEY,
    token      BYTEA        NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL
);

CREATE TABLE dep_cursors (
    account       VARCHAR(255) NOT NULL PRIMARY KEY,
    value         TEXT         NOT NULL,
    phase         VARCHAR(16)  NOT NULL,
    fetched_until TIMESTAMPTZ  NULL,
    updated_at    TIMESTAMPTZ  NOT NULL
);

CREATE TABLE dep_keypairs (
    account    VARCHAR(255) NOT NULL,
    stage      VARCHAR(16)  NOT NULL,
    cert_pem   BYTEA        NOT NULL,
    key_pem    BYTEA        NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (account, stage)
);

CREATE TABLE dep_devices (
    account        VARCHAR(255) NOT NULL,
    serial_number  VARCHAR(255) NOT NULL,
    record         BYTEA        NOT NULL,
    profile_uuid   VARCHAR(255) NOT NULL DEFAULT '',
    profile_status VARCHAR(32)  NOT NULL DEFAULT '',
    op_type        VARCHAR(32)  NOT NULL DEFAULT '',
    op_date        TIMESTAMPTZ  NULL,
    deleted        BOOLEAN      NOT NULL DEFAULT FALSE,
    first_seen     TIMESTAMPTZ  NOT NULL,
    updated_at     TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (account, serial_number)
);
CREATE INDEX idx_dep_devices_profile ON dep_devices (account, profile_uuid, serial_number);

CREATE TABLE dep_profiles (
    account      VARCHAR(255) NOT NULL,
    profile_uuid VARCHAR(255) NOT NULL,
    record       BYTEA        NOT NULL,
    updated_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (account, profile_uuid)
);

CREATE TABLE dep_assignments (
    account         VARCHAR(255) NOT NULL,
    serial_number   VARCHAR(255) NOT NULL,
    profile_uuid    VARCHAR(255) NOT NULL DEFAULT '',
    status          VARCHAR(32)  NOT NULL,
    attempts        INTEGER      NOT NULL DEFAULT 0,
    last_error      TEXT         NOT NULL DEFAULT '',
    attempted_at    TIMESTAMPTZ  NULL,
    next_attempt_at TIMESTAMPTZ  NULL,
    PRIMARY KEY (account, serial_number)
);
CREATE INDEX idx_dep_assignments_status ON dep_assignments (account, status, serial_number);

-- +down
DROP TABLE dep_assignments;
DROP TABLE dep_profiles;
DROP TABLE dep_devices;
DROP TABLE dep_keypairs;
DROP TABLE dep_cursors;
DROP TABLE dep_sessions;
DROP TABLE dep_accounts;
