-- +up
CREATE TABLE audit_records (
    id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    at            DATETIME(6)  NOT NULL,
    type          VARCHAR(64)  NOT NULL,
    actor         VARCHAR(64)  NOT NULL DEFAULT '',
    channel       VARCHAR(32)  NOT NULL DEFAULT '',
    enrollment_id VARCHAR(255) NOT NULL DEFAULT '',
    parent_id     VARCHAR(255) NOT NULL DEFAULT '',
    fields        MEDIUMTEXT   NOT NULL,
    INDEX idx_audit_records_at (at DESC, id DESC),
    INDEX idx_audit_records_enrollment (enrollment_id, id DESC),
    INDEX idx_audit_records_actor (actor, id DESC),
    INDEX idx_audit_records_type (type, id DESC)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE audit_records;
