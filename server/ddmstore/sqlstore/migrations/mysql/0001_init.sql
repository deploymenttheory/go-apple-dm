-- +up
CREATE TABLE ddm_declarations (
    identifier   VARCHAR(255) NOT NULL PRIMARY KEY,
    type         VARCHAR(255) NOT NULL,
    kind         VARCHAR(32)  NOT NULL,
    server_token VARCHAR(255) NOT NULL,
    canonical    MEDIUMBLOB   NOT NULL,
    created_at   DATETIME(6)  NOT NULL,
    updated_at   DATETIME(6)  NOT NULL,
    INDEX idx_ddm_declarations_kind (kind, identifier),
    INDEX idx_ddm_declarations_type (type)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_declaration_versions (
    identifier   VARCHAR(255) NOT NULL,
    server_token VARCHAR(255) NOT NULL,
    type         VARCHAR(255) NOT NULL,
    canonical    MEDIUMBLOB   NOT NULL,
    created_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (identifier, server_token),
    CONSTRAINT fk_ddm_versions_declaration FOREIGN KEY (identifier) REFERENCES ddm_declarations (identifier) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_sets (
    name       VARCHAR(255) NOT NULL PRIMARY KEY,
    created_at DATETIME(6)  NOT NULL,
    updated_at DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_set_declarations (
    set_name   VARCHAR(255) NOT NULL,
    identifier VARCHAR(255) NOT NULL,
    added_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (set_name, identifier),
    INDEX idx_ddm_set_declarations_identifier (identifier),
    CONSTRAINT fk_ddm_set_declarations_set FOREIGN KEY (set_name) REFERENCES ddm_sets (name) ON DELETE CASCADE,
    CONSTRAINT fk_ddm_set_declarations_declaration FOREIGN KEY (identifier) REFERENCES ddm_declarations (identifier) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_enrollment_sets (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    set_name      VARCHAR(255) NOT NULL,
    assigned_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (enrollment_id, set_name),
    INDEX idx_ddm_enrollment_sets_set (set_name, enrollment_id),
    CONSTRAINT fk_ddm_enrollment_sets_set FOREIGN KEY (set_name) REFERENCES ddm_sets (name) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_enrollment_declarations (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    identifier    VARCHAR(255) NOT NULL,
    assigned_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (enrollment_id, identifier),
    INDEX idx_ddm_enrollment_declarations_identifier (identifier, enrollment_id),
    CONSTRAINT fk_ddm_enrollment_declarations_declaration FOREIGN KEY (identifier) REFERENCES ddm_declarations (identifier) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_snapshots (
    enrollment_id      VARCHAR(255) NOT NULL PRIMARY KEY,
    channel            SMALLINT     NOT NULL,
    parent_id          VARCHAR(255) NOT NULL DEFAULT '',
    declarations_token VARCHAR(255) NOT NULL,
    token_changed_at   DATETIME(6)  NOT NULL,
    refreshed_at       DATETIME(6)  NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_snapshot_items (
    enrollment_id VARCHAR(255) NOT NULL,
    kind          VARCHAR(32)  NOT NULL,
    identifier    VARCHAR(255) NOT NULL,
    server_token  VARCHAR(255) NOT NULL,
    base_token    VARCHAR(255) NOT NULL,
    expanded      MEDIUMBLOB   NULL,
    pos           INTEGER      NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier),
    INDEX idx_ddm_snapshot_items_version (identifier, base_token),
    CONSTRAINT fk_ddm_snapshot_items_snapshot FOREIGN KEY (enrollment_id) REFERENCES ddm_snapshots (enrollment_id) ON DELETE CASCADE
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_status_declarations (
    enrollment_id VARCHAR(255) NOT NULL,
    channel       SMALLINT     NOT NULL,
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    kind          VARCHAR(32)  NOT NULL,
    identifier    VARCHAR(255) NOT NULL,
    server_token  VARCHAR(255) NOT NULL,
    active        BOOLEAN      NOT NULL,
    valid         VARCHAR(32)  NOT NULL,
    reasons       BLOB         NULL,
    first_seen    DATETIME(6)  NOT NULL,
    last_seen     DATETIME(6)  NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier),
    INDEX idx_ddm_status_declarations_identifier (identifier, enrollment_id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_status_values (
    enrollment_id VARCHAR(255) NOT NULL,
    path          VARCHAR(500) NOT NULL,
    value         BLOB         NOT NULL,
    first_seen    DATETIME(6)  NOT NULL,
    last_seen     DATETIME(6)  NOT NULL,
    PRIMARY KEY (enrollment_id, path),
    INDEX idx_ddm_status_values_path (path, enrollment_id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_status_errors (
    seq           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    enrollment_id VARCHAR(255) NOT NULL,
    status_item   VARCHAR(255) NOT NULL,
    reasons       BLOB         NULL,
    received_at   DATETIME(6)  NOT NULL,
    INDEX idx_ddm_status_errors_enrollment (enrollment_id, seq)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_status_reports (
    seq           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    enrollment_id VARCHAR(255) NOT NULL,
    full_report   BOOLEAN      NOT NULL,
    raw           MEDIUMBLOB   NULL,
    received_at   DATETIME(6)  NOT NULL,
    INDEX idx_ddm_status_reports_enrollment (enrollment_id, seq)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

CREATE TABLE ddm_changes (
    seq             BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    enrollment_id   VARCHAR(255) NOT NULL,
    channel         SMALLINT     NOT NULL,
    parent_id       VARCHAR(255) NOT NULL DEFAULT '',
    reason          VARCHAR(255) NOT NULL,
    created_at      DATETIME(6)  NOT NULL,
    attempts        INTEGER      NOT NULL DEFAULT 0,
    last_error      TEXT         NOT NULL,
    next_attempt_at DATETIME(6)  NOT NULL,
    INDEX idx_ddm_changes_due (next_attempt_at, seq),
    INDEX idx_ddm_changes_enrollment (enrollment_id, seq)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

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
