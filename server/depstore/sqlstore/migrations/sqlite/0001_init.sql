-- +up
CREATE TABLE dep_accounts (
    name                TEXT      NOT NULL PRIMARY KEY,
    consumer_key        TEXT      NOT NULL DEFAULT '',
    consumer_secret     BLOB      NULL,
    access_token        BLOB      NULL,
    access_secret       BLOB      NULL,
    access_token_expiry TIMESTAMP NULL,
    protocol_version    INTEGER   NOT NULL DEFAULT 0,
    org_name            TEXT      NOT NULL DEFAULT '',
    org_id              TEXT      NOT NULL DEFAULT '',
    server_name         TEXT      NOT NULL DEFAULT '',
    server_uuid         TEXT      NOT NULL DEFAULT '',
    admin_id            TEXT      NOT NULL DEFAULT '',
    limits              BLOB      NULL,
    profile_uuid        TEXT      NOT NULL DEFAULT '',
    terms_expired       BOOLEAN   NOT NULL DEFAULT 0,
    token_invalid       BOOLEAN   NOT NULL DEFAULT 0,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);

CREATE TABLE dep_sessions (
    account    TEXT      NOT NULL PRIMARY KEY,
    token      BLOB      NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE dep_cursors (
    account       TEXT      NOT NULL PRIMARY KEY,
    value         TEXT      NOT NULL,
    phase         TEXT      NOT NULL,
    fetched_until TIMESTAMP NULL,
    updated_at    TIMESTAMP NOT NULL
);

CREATE TABLE dep_keypairs (
    account    TEXT      NOT NULL,
    stage      TEXT      NOT NULL,
    cert_pem   BLOB      NOT NULL,
    key_pem    BLOB      NOT NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (account, stage)
);

CREATE TABLE dep_devices (
    account        TEXT      NOT NULL,
    serial_number  TEXT      NOT NULL,
    record         BLOB      NOT NULL,
    profile_uuid   TEXT      NOT NULL DEFAULT '',
    profile_status TEXT      NOT NULL DEFAULT '',
    op_type        TEXT      NOT NULL DEFAULT '',
    op_date        TIMESTAMP NULL,
    deleted        BOOLEAN   NOT NULL DEFAULT 0,
    first_seen     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL,
    PRIMARY KEY (account, serial_number)
);
CREATE INDEX idx_dep_devices_profile ON dep_devices (account, profile_uuid, serial_number);

CREATE TABLE dep_profiles (
    account      TEXT      NOT NULL,
    profile_uuid TEXT      NOT NULL,
    record       BLOB      NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (account, profile_uuid)
);

CREATE TABLE dep_assignments (
    account         TEXT      NOT NULL,
    serial_number   TEXT      NOT NULL,
    profile_uuid    TEXT      NOT NULL DEFAULT '',
    status          TEXT      NOT NULL,
    attempts        INTEGER   NOT NULL DEFAULT 0,
    last_error      TEXT      NOT NULL DEFAULT '',
    attempted_at    TIMESTAMP NULL,
    next_attempt_at TIMESTAMP NULL,
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
