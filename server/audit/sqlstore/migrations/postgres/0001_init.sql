-- +up
CREATE TABLE audit_records (
    id            BIGSERIAL   NOT NULL PRIMARY KEY,
    at            TIMESTAMPTZ NOT NULL,
    type          VARCHAR(64) NOT NULL,
    actor         VARCHAR(64) NOT NULL DEFAULT '',
    channel       VARCHAR(32) NOT NULL DEFAULT '',
    enrollment_id VARCHAR(255) NOT NULL DEFAULT '',
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    fields        TEXT        NOT NULL DEFAULT ''
);

-- The trail is read newest first and pruned by age, so both paths are the
-- same descending scan on (at, id). The id is in the index because it is the
-- pagination cursor and breaks ties between records sharing a timestamp.
CREATE INDEX idx_audit_records_at ON audit_records (at DESC, id DESC);
-- "What did this enrollment do" and "what did this actor do" are the two
-- questions the trail exists to answer.
CREATE INDEX idx_audit_records_enrollment ON audit_records (enrollment_id, id DESC);
CREATE INDEX idx_audit_records_actor ON audit_records (actor, id DESC);
CREATE INDEX idx_audit_records_type ON audit_records (type, id DESC);

-- +down
DROP TABLE audit_records;
