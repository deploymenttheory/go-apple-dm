-- +up
CREATE TABLE admin_principals (
    name         TEXT      NOT NULL PRIMARY KEY,
    roles        TEXT      NOT NULL DEFAULT '',
    root         INTEGER   NOT NULL DEFAULT 0,
    token_digest TEXT      NULL,
    token_id     TEXT      NOT NULL DEFAULT '',
    token_at     TIMESTAMP NULL,
    expires_at   TIMESTAMP NULL,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);
-- The authentication path is one indexed lookup on the digest. A revoked
-- principal stores NULL rather than an empty string, so the partial index
-- allows many revoked rows while keeping live digests unique.
CREATE UNIQUE INDEX idx_admin_principals_digest ON admin_principals (token_digest) WHERE token_digest IS NOT NULL;
CREATE INDEX idx_admin_principals_root ON admin_principals (root);

CREATE TABLE admin_policies (
    name        TEXT      NOT NULL PRIMARY KEY,
    source      TEXT      NOT NULL,
    description TEXT      NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);

-- One row, bumped inside every policy write, so a compiled policy set can
-- tell it is stale without recompiling on each request.
CREATE TABLE admin_policy_version (
    id      INTEGER NOT NULL PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 0
);
INSERT INTO admin_policy_version (id, version) VALUES (1, 0);

-- +down
DROP TABLE admin_policy_version;
DROP TABLE admin_policies;
DROP TABLE admin_principals;
