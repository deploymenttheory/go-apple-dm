-- +up
CREATE TABLE maintenance_state (
    id INTEGER PRIMARY KEY,
    token VARCHAR(128) NOT NULL
);
INSERT INTO maintenance_state (id, token) VALUES (1, '');
CREATE TABLE maintenance_participants (
    id VARCHAR(64) PRIMARY KEY,
    label VARCHAR(255) NOT NULL,
    drained_token VARCHAR(128) NOT NULL
);
-- +down
DROP TABLE maintenance_participants;
DROP TABLE maintenance_state;
