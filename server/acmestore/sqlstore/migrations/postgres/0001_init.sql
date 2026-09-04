-- +up
CREATE TABLE acme_accounts (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    thumbprint VARCHAR(255) NOT NULL,
    status     VARCHAR(32)  NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NULL,
    record     BYTEA        NOT NULL
);
CREATE UNIQUE INDEX idx_acme_accounts_thumbprint ON acme_accounts (thumbprint);

CREATE TABLE acme_orders (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL DEFAULT '',
    authz_id   VARCHAR(255) NOT NULL DEFAULT '',
    status     VARCHAR(32)  NOT NULL DEFAULT '',
    expires    TIMESTAMPTZ  NULL,
    created_at TIMESTAMPTZ  NULL,
    record     BYTEA        NOT NULL
);
CREATE INDEX idx_acme_orders_account ON acme_orders (account_id, id);
CREATE INDEX idx_acme_orders_expires ON acme_orders (expires);

CREATE TABLE acme_authorizations (
    id       VARCHAR(255) NOT NULL PRIMARY KEY,
    order_id VARCHAR(255) NOT NULL DEFAULT '',
    expires  TIMESTAMPTZ  NULL,
    record   BYTEA        NOT NULL
);
CREATE INDEX idx_acme_authorizations_order ON acme_authorizations (order_id);
CREATE INDEX idx_acme_authorizations_expires ON acme_authorizations (expires);

CREATE TABLE acme_challenges (
    id       VARCHAR(255) NOT NULL PRIMARY KEY,
    authz_id VARCHAR(255) NOT NULL DEFAULT '',
    record   BYTEA        NOT NULL
);
CREATE INDEX idx_acme_challenges_authz ON acme_challenges (authz_id);

CREATE TABLE acme_certificates (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL DEFAULT '',
    order_id   VARCHAR(255) NOT NULL DEFAULT '',
    serial     VARCHAR(255) NOT NULL DEFAULT '',
    udid       VARCHAR(255) NOT NULL DEFAULT '',
    not_after  TIMESTAMPTZ  NULL,
    issued_at  TIMESTAMPTZ  NULL,
    record     BYTEA        NOT NULL
);
CREATE INDEX idx_acme_certificates_account ON acme_certificates (account_id, id);
CREATE INDEX idx_acme_certificates_serial ON acme_certificates (serial, id);
CREATE INDEX idx_acme_certificates_udid ON acme_certificates (udid, id);

CREATE TABLE acme_nonces (
    value     VARCHAR(255) NOT NULL PRIMARY KEY,
    issued_at TIMESTAMPTZ  NULL
);
CREATE INDEX idx_acme_nonces_issued ON acme_nonces (issued_at);

CREATE TABLE acme_claims (
    identifier VARCHAR(255) NOT NULL PRIMARY KEY,
    order_id   VARCHAR(255) NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ  NOT NULL
);

-- +down
DROP TABLE acme_claims;
DROP TABLE acme_nonces;
DROP TABLE acme_certificates;
DROP TABLE acme_challenges;
DROP TABLE acme_authorizations;
DROP TABLE acme_orders;
DROP TABLE acme_accounts;
