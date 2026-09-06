-- +up
CREATE TABLE ddm_declarations (
    identifier   TEXT      NOT NULL PRIMARY KEY,
    type         TEXT      NOT NULL,
    kind         TEXT      NOT NULL,
    server_token TEXT      NOT NULL,
    canonical    BLOB      NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_declarations_kind ON ddm_declarations (kind, identifier);
CREATE INDEX idx_ddm_declarations_type ON ddm_declarations (type);

CREATE TABLE ddm_declaration_versions (
    identifier   TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    server_token TEXT      NOT NULL,
    type         TEXT      NOT NULL,
    canonical    BLOB      NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (identifier, server_token)
);

CREATE TABLE ddm_sets (
    name       TEXT      NOT NULL PRIMARY KEY,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE ddm_set_declarations (
    set_name   TEXT      NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    identifier TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    added_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (set_name, identifier)
);
CREATE INDEX idx_ddm_set_declarations_identifier ON ddm_set_declarations (identifier);

CREATE TABLE ddm_enrollment_sets (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    set_name      TEXT      NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    assigned_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, set_name)
);
CREATE INDEX idx_ddm_enrollment_sets_set ON ddm_enrollment_sets (set_name, enrollment_id);

CREATE TABLE ddm_enrollment_declarations (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    identifier    TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    assigned_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, identifier)
);
CREATE INDEX idx_ddm_enrollment_declarations_identifier ON ddm_enrollment_declarations (identifier, enrollment_id);

CREATE TABLE ddm_snapshots (
    enrollment_id      TEXT      NOT NULL PRIMARY KEY,
    channel            INTEGER   NOT NULL,
    parent_id          TEXT      NOT NULL DEFAULT '',
    declarations_token TEXT      NOT NULL,
    token_changed_at   TIMESTAMP NOT NULL,
    refreshed_at       TIMESTAMP NOT NULL
);

CREATE TABLE ddm_snapshot_items (
    enrollment_id TEXT    NOT NULL REFERENCES ddm_snapshots (enrollment_id) ON DELETE CASCADE,
    kind          TEXT    NOT NULL,
    identifier    TEXT    NOT NULL,
    server_token  TEXT    NOT NULL,
    base_token    TEXT    NOT NULL,
    expanded      BLOB    NULL,
    pos           INTEGER NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_snapshot_items_version ON ddm_snapshot_items (identifier, base_token);

CREATE TABLE ddm_status_declarations (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    kind          TEXT      NOT NULL,
    identifier    TEXT      NOT NULL,
    server_token  TEXT      NOT NULL,
    active        INTEGER   NOT NULL,
    valid         TEXT      NOT NULL,
    reasons       BLOB      NULL,
    first_seen    TIMESTAMP NOT NULL,
    last_seen     TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_status_declarations_identifier ON ddm_status_declarations (identifier, enrollment_id);

CREATE TABLE ddm_status_values (
    enrollment_id TEXT      NOT NULL,
    path          TEXT      NOT NULL,
    value         BLOB      NOT NULL,
    first_seen    TIMESTAMP NOT NULL,
    last_seen     TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, path)
);
CREATE INDEX idx_ddm_status_values_path ON ddm_status_values (path, enrollment_id);

CREATE TABLE ddm_status_errors (
    seq           INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id TEXT      NOT NULL,
    status_item   TEXT      NOT NULL,
    reasons       BLOB      NULL,
    received_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_status_errors_enrollment ON ddm_status_errors (enrollment_id, seq);

CREATE TABLE ddm_status_reports (
    seq           INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id TEXT      NOT NULL,
    full_report   INTEGER   NOT NULL,
    raw           BLOB      NULL,
    received_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_status_reports_enrollment ON ddm_status_reports (enrollment_id, seq);

CREATE TABLE ddm_changes (
    seq             INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id   TEXT      NOT NULL,
    channel         INTEGER   NOT NULL,
    parent_id       TEXT      NOT NULL DEFAULT '',
    reason          TEXT      NOT NULL,
    created_at      TIMESTAMP NOT NULL,
    attempts        INTEGER   NOT NULL DEFAULT 0,
    last_error      TEXT      NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_changes_due ON ddm_changes (next_attempt_at, seq);
CREATE INDEX idx_ddm_changes_enrollment ON ddm_changes (enrollment_id, seq);

-- +down
DROP TABLE ddm_changes;
DROP TABLE ddm_status_reports;
DROP TABLE ddm_status_errors;
DROP TABLE ddm_status_values;
DROP TABLE ddm_status_declarations;
DROP TABLE ddm_snapshot_items;
DROP TABLE ddm_snapshots;
DROP TABLE ddm_enrollment_declarations;
DROP TABLE ddm_enrollment_sets;
DROP TABLE ddm_set_declarations;
DROP TABLE ddm_sets;
DROP TABLE ddm_declaration_versions;
DROP TABLE ddm_declarations;
