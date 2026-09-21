-- +up
ALTER TABLE dep_cursors ADD COLUMN revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE dep_cursors ADD COLUMN generation TEXT NOT NULL DEFAULT '';
ALTER TABLE dep_devices ADD COLUMN fetch_generation TEXT NOT NULL DEFAULT '';
CREATE TABLE dep_assignment_state (
    account TEXT NOT NULL PRIMARY KEY,
    owner TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMP NULL,
    not_before TIMESTAMP NULL,
    failures INTEGER NOT NULL DEFAULT 0
);

-- +down
DROP TABLE dep_assignment_state;
ALTER TABLE dep_devices DROP COLUMN fetch_generation;
ALTER TABLE dep_cursors DROP COLUMN generation;
ALTER TABLE dep_cursors DROP COLUMN revision;
