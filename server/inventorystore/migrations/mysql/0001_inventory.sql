-- +up
CREATE TABLE inventory_entries (
    entry_key VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    payload LONGBLOB NOT NULL
);
CREATE TABLE inventory_lock (
    id INTEGER NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL
);
INSERT INTO inventory_lock (id, version) VALUES (1, 0);
-- +down
DROP TABLE inventory_entries;
DROP TABLE inventory_lock;
