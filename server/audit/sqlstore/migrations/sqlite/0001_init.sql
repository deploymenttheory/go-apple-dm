-- +up
CREATE TABLE audit_records (
    id            INTEGER     NOT NULL PRIMARY KEY AUTOINCREMENT,
    at            TIMESTAMP   NOT NULL,
    type          VARCHAR(64) NOT NULL,
    actor         VARCHAR(64) NOT NULL DEFAULT '',
    channel       VARCHAR(32) NOT NULL DEFAULT '',
    enrollment_id VARCHAR(255) NOT NULL DEFAULT '',
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    fields        TEXT        NOT NULL DEFAULT ''
);

-- AUTOINCREMENT rather than a bare INTEGER PRIMARY KEY so a pruned id is
-- never reused: the cursor and the ordering both depend on ids rising.
CREATE INDEX idx_audit_records_at ON audit_records (at DESC, id DESC);
CREATE INDEX idx_audit_records_enrollment ON audit_records (enrollment_id, id DESC);
CREATE INDEX idx_audit_records_actor ON audit_records (actor, id DESC);
CREATE INDEX idx_audit_records_type ON audit_records (type, id DESC);

-- +down
DROP TABLE audit_records;
