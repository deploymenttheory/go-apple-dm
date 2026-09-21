-- +up
ALTER TABLE dep_cursors ADD COLUMN revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE dep_cursors ADD COLUMN generation VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE dep_devices ADD COLUMN fetch_generation VARCHAR(255) NOT NULL DEFAULT '';
CREATE TABLE dep_assignment_state (
    account VARCHAR(255) NOT NULL PRIMARY KEY,
    owner VARCHAR(255) NOT NULL DEFAULT '',
    lease_until DATETIME(6) NULL,
    not_before DATETIME(6) NULL,
    failures INTEGER NOT NULL DEFAULT 0
);

-- +down
DROP TABLE dep_assignment_state;
ALTER TABLE dep_devices DROP COLUMN fetch_generation;
ALTER TABLE dep_cursors DROP COLUMN generation;
ALTER TABLE dep_cursors DROP COLUMN revision;
