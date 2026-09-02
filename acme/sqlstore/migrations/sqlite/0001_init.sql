-- +up
CREATE TABLE acme_accounts (
    id         TEXT      NOT NULL PRIMARY KEY,
    thumbprint TEXT      NOT NULL,
    status     TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NULL,
    record     BLOB      NOT NULL
);
CREATE UNIQUE INDEX idx_acme_accounts_thumbprint ON acme_accounts (thumbprint);

CREATE TABLE acme_orders (
    id         TEXT      NOT NULL PRIMARY KEY,
    account_id TEXT      NOT NULL DEFAULT '',
    authz_id   TEXT      NOT NULL DEFAULT '',
    status     TEXT      NOT NULL DEFAULT '',
    expires    TIMESTAMP NULL,
    created_at TIMESTAMP NULL,
    record     BLOB      NOT NULL
);
CREATE INDEX idx_acme_orders_account ON acme_orders (account_id, id);
CREATE INDEX idx_acme_orders_expires ON acme_orders (expires);

CREATE TABLE acme_authorizations (
    id       TEXT      NOT NULL PRIMARY KEY,
    order_id TEXT      NOT NULL DEFAULT '',
    expires  TIMESTAMP NULL,
    record   BLOB      NOT NULL
);
CREATE INDEX idx_acme_authorizations_order ON acme_authorizations (order_id);
CREATE INDEX idx_acme_authorizations_expires ON acme_authorizations (expires);

CREATE TABLE acme_challenges (
    id       TEXT NOT NULL PRIMARY KEY,
    authz_id TEXT NOT NULL DEFAULT '',
    record   BLOB NOT NULL
);
CREATE INDEX idx_acme_challenges_authz ON acme_challenges (authz_id);

CREATE TABLE acme_certificates (
    id         TEXT      NOT NULL PRIMARY KEY,
    account_id TEXT      NOT NULL DEFAULT '',
    order_id   TEXT      NOT NULL DEFAULT '',
    serial     TEXT      NOT NULL DEFAULT '',
    udid       TEXT      NOT NULL DEFAULT '',
    not_after  TIMESTAMP NULL,
    issued_at  TIMESTAMP NULL,
    record     BLOB      NOT NULL
);
CREATE INDEX idx_acme_certificates_account ON acme_certificates (account_id, id);
CREATE INDEX idx_acme_certificates_serial ON acme_certificates (serial, id);
CREATE INDEX idx_acme_certificates_udid ON acme_certificates (udid, id);

CREATE TABLE acme_nonces (
    value     TEXT      NOT NULL PRIMARY KEY,
    issued_at TIMESTAMP NULL
);
CREATE INDEX idx_acme_nonces_issued ON acme_nonces (issued_at);

CREATE TABLE acme_claims (
    identifier TEXT      NOT NULL PRIMARY KEY,
    order_id   TEXT      NOT NULL DEFAULT '',
    claimed_at TIMESTAMP NOT NULL
);

-- +down
DROP TABLE acme_claims;
DROP TABLE acme_nonces;
DROP TABLE acme_certificates;
DROP TABLE acme_challenges;
DROP TABLE acme_authorizations;
DROP TABLE acme_orders;
DROP TABLE acme_accounts;
