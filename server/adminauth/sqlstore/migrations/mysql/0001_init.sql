-- +up
CREATE TABLE admin_principals (
    name         VARCHAR(64)  NOT NULL PRIMARY KEY,
    roles        TEXT         NOT NULL,
    root         BOOLEAN      NOT NULL DEFAULT FALSE,
    token_digest VARCHAR(64)  NULL,
    token_id     VARCHAR(32)  NOT NULL DEFAULT '',
    token_at     DATETIME(6)  NULL,
    expires_at   DATETIME(6)  NULL,
    created_at   DATETIME(6)  NOT NULL,
    updated_at   DATETIME(6)  NOT NULL,
    -- The authentication path is one indexed lookup on the digest. A revoked
    -- principal stores NULL rather than an empty string, and NULLs are
    -- distinct in a unique index, so many revoked rows coexist.
    UNIQUE INDEX idx_admin_principals_digest (token_digest),
    INDEX idx_admin_principals_root (root)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE admin_policies (
    name        VARCHAR(64) NOT NULL PRIMARY KEY,
    source      MEDIUMTEXT  NOT NULL,
    description TEXT        NOT NULL,
    created_at  DATETIME(6) NOT NULL,
    updated_at  DATETIME(6) NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- One row, bumped inside every policy write, so a compiled policy set can
-- tell it is stale without recompiling on each request.
CREATE TABLE admin_policy_version (
    id      SMALLINT NOT NULL PRIMARY KEY,
    version BIGINT   NOT NULL DEFAULT 0
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
INSERT INTO admin_policy_version (id, version) VALUES (1, 0);

-- +down
DROP TABLE admin_policy_version;
DROP TABLE admin_policies;
DROP TABLE admin_principals;
