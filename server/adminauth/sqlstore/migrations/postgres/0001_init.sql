-- +up
CREATE TABLE admin_principals (
    name         VARCHAR(64)  NOT NULL PRIMARY KEY,
    roles        TEXT         NOT NULL DEFAULT '',
    root         BOOLEAN      NOT NULL DEFAULT FALSE,
    token_digest VARCHAR(64)  NULL,
    token_id     VARCHAR(32)  NOT NULL DEFAULT '',
    token_at     TIMESTAMPTZ  NULL,
    expires_at   TIMESTAMPTZ  NULL,
    created_at   TIMESTAMPTZ  NOT NULL,
    updated_at   TIMESTAMPTZ  NOT NULL
);
-- The authentication path is one indexed lookup on the digest. A revoked
-- principal stores NULL rather than an empty string, and NULLs are distinct
-- in a unique index, so many revoked rows coexist.
CREATE UNIQUE INDEX idx_admin_principals_digest ON admin_principals (token_digest);
CREATE INDEX idx_admin_principals_root ON admin_principals (root);

CREATE TABLE admin_policies (
    name        VARCHAR(64) NOT NULL PRIMARY KEY,
    source      TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

-- One row, bumped inside every policy write, so a compiled policy set can
-- tell it is stale without recompiling on each request.
CREATE TABLE admin_policy_version (
    id      SMALLINT NOT NULL PRIMARY KEY,
    version BIGINT   NOT NULL DEFAULT 0
);
INSERT INTO admin_policy_version (id, version) VALUES (1, 0);

-- +down
DROP TABLE admin_policy_version;
DROP TABLE admin_policies;
DROP TABLE admin_principals;
