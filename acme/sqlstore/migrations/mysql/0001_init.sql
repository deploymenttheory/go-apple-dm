-- +up
CREATE TABLE acme_accounts (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    thumbprint VARCHAR(255) NOT NULL,
    status     VARCHAR(32)  NOT NULL DEFAULT '',
    created_at DATETIME(6)  NULL,
    record     MEDIUMBLOB   NOT NULL,
    UNIQUE KEY idx_acme_accounts_thumbprint (thumbprint)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_orders (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL DEFAULT '',
    authz_id   VARCHAR(255) NOT NULL DEFAULT '',
    status     VARCHAR(32)  NOT NULL DEFAULT '',
    expires    DATETIME(6)  NULL,
    created_at DATETIME(6)  NULL,
    record     MEDIUMBLOB   NOT NULL,
    INDEX idx_acme_orders_account (account_id, id),
    INDEX idx_acme_orders_expires (expires)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_authorizations (
    id       VARCHAR(255) NOT NULL PRIMARY KEY,
    order_id VARCHAR(255) NOT NULL DEFAULT '',
    expires  DATETIME(6)  NULL,
    record   MEDIUMBLOB   NOT NULL,
    INDEX idx_acme_authorizations_order (order_id),
    INDEX idx_acme_authorizations_expires (expires)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_challenges (
    id       VARCHAR(255) NOT NULL PRIMARY KEY,
    authz_id VARCHAR(255) NOT NULL DEFAULT '',
    record   MEDIUMBLOB   NOT NULL,
    INDEX idx_acme_challenges_authz (authz_id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_certificates (
    id         VARCHAR(255) NOT NULL PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL DEFAULT '',
    order_id   VARCHAR(255) NOT NULL DEFAULT '',
    serial     VARCHAR(255) NOT NULL DEFAULT '',
    udid       VARCHAR(255) NOT NULL DEFAULT '',
    not_after  DATETIME(6)  NULL,
    issued_at  DATETIME(6)  NULL,
    record     MEDIUMBLOB   NOT NULL,
    INDEX idx_acme_certificates_account (account_id, id),
    INDEX idx_acme_certificates_serial (serial, id),
    INDEX idx_acme_certificates_udid (udid, id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_nonces (
    value     VARCHAR(255) NOT NULL PRIMARY KEY,
    issued_at DATETIME(6)  NULL,
    INDEX idx_acme_nonces_issued (issued_at)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE acme_claims (
    identifier VARCHAR(255) NOT NULL PRIMARY KEY,
    order_id   VARCHAR(255) NOT NULL DEFAULT '',
    claimed_at DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE acme_claims;
DROP TABLE acme_nonces;
DROP TABLE acme_certificates;
DROP TABLE acme_challenges;
DROP TABLE acme_authorizations;
DROP TABLE acme_orders;
DROP TABLE acme_accounts;
