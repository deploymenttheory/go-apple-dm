-- +up
CREATE TABLE ddm_declarations (
    identifier   VARCHAR(255) NOT NULL PRIMARY KEY,
    type         VARCHAR(255) NOT NULL,
    kind         VARCHAR(32)  NOT NULL,
    server_token VARCHAR(255) NOT NULL,
    canonical    BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL,
    updated_at   TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_ddm_declarations_kind ON ddm_declarations (kind, identifier);
CREATE INDEX idx_ddm_declarations_type ON ddm_declarations (type);

CREATE TABLE ddm_declaration_versions (
    identifier   VARCHAR(255) NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    server_token VARCHAR(255) NOT NULL,
    type         VARCHAR(255) NOT NULL,
    canonical    BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (identifier, server_token)
);

CREATE TABLE ddm_sets (
    name       VARCHAR(255) NOT NULL PRIMARY KEY,
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL
);

CREATE TABLE ddm_set_declarations (
    set_name   VARCHAR(255) NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    identifier VARCHAR(255) NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    added_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (set_name, identifier)
);
CREATE INDEX idx_ddm_set_declarations_identifier ON ddm_set_declarations (identifier);

CREATE TABLE ddm_enrollment_sets (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    set_name      VARCHAR(255) NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    assigned_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (enrollment_id, set_name)
);
CREATE INDEX idx_ddm_enrollment_sets_set ON ddm_enrollment_sets (set_name, enrollment_id);

CREATE TABLE ddm_enrollment_declarations (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    identifier    VARCHAR(255) NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    assigned_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (enrollment_id, identifier)
);
CREATE INDEX idx_ddm_enrollment_declarations_identifier ON ddm_enrollment_declarations (identifier, enrollment_id);

CREATE TABLE ddm_snapshots (
    enrollment_id      VARCHAR(255) NOT NULL PRIMARY KEY,
    channel            SMALLINT     NOT NULL,
    parent_id          VARCHAR(255) NOT NULL DEFAULT '',
    declarations_token VARCHAR(255) NOT NULL,
    token_changed_at   TIMESTAMPTZ  NOT NULL,
    refreshed_at       TIMESTAMPTZ  NOT NULL
);

CREATE TABLE ddm_snapshot_items (
    enrollment_id VARCHAR(255) NOT NULL REFERENCES ddm_snapshots (enrollment_id) ON DELETE CASCADE,
    kind          VARCHAR(32)  NOT NULL,
    identifier    VARCHAR(255) NOT NULL,
    server_token  VARCHAR(255) NOT NULL,
    base_token    VARCHAR(255) NOT NULL,
    expanded      BYTEA        NULL,
    pos           INTEGER      NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_snapshot_items_version ON ddm_snapshot_items (identifier, base_token);

CREATE TABLE ddm_status_declarations (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    kind          VARCHAR(32)  NOT NULL,
    identifier    VARCHAR(255) NOT NULL,
    server_token  VARCHAR(255) NOT NULL,
    active        BOOLEAN      NOT NULL,
    valid         VARCHAR(32)  NOT NULL,
    reasons       BYTEA        NULL,
    first_seen    TIMESTAMPTZ  NOT NULL,
    last_seen     TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_status_declarations_identifier ON ddm_status_declarations (identifier, enrollment_id);

CREATE TABLE ddm_status_values (
    enrollment_id VARCHAR(255) NOT NULL,
    path          VARCHAR(500) NOT NULL,
    value         BYTEA        NOT NULL,
    first_seen    TIMESTAMPTZ  NOT NULL,
    last_seen     TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (enrollment_id, path)
);
CREATE INDEX idx_ddm_status_values_path ON ddm_status_values (path, enrollment_id);

CREATE TABLE ddm_status_errors (
    seq           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    enrollment_id VARCHAR(255) NOT NULL,
    status_item   VARCHAR(255) NOT NULL,
    reasons       BYTEA        NULL,
    received_at   TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_ddm_status_errors_enrollment ON ddm_status_errors (enrollment_id, seq);

CREATE TABLE ddm_status_reports (
    seq           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    enrollment_id VARCHAR(255) NOT NULL,
    full_report   BOOLEAN      NOT NULL,
    raw           BYTEA        NULL,
    received_at   TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_ddm_status_reports_enrollment ON ddm_status_reports (enrollment_id, seq);

CREATE TABLE ddm_changes (
    seq             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    enrollment_id   VARCHAR(255) NOT NULL,
    channel         SMALLINT     NOT NULL,
    parent_id       VARCHAR(255) NOT NULL DEFAULT '',
    reason          VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL,
    attempts        INTEGER      NOT NULL DEFAULT 0,
    last_error      TEXT         NOT NULL,
    next_attempt_at TIMESTAMPTZ  NOT NULL
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
