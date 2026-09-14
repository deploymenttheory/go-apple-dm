-- +up
CREATE TABLE event_records (
    event_id VARCHAR(64) PRIMARY KEY,
    type VARCHAR(128) NOT NULL,
    occurred_at BIGINT NOT NULL,
    payload TEXT NOT NULL
);
CREATE TABLE event_deliveries (
    event_id VARCHAR(64) NOT NULL,
    destination VARCHAR(128) NOT NULL,
    state VARCHAR(16) NOT NULL,
    attempts INTEGER NOT NULL,
    next_attempt BIGINT NOT NULL,
    lease_token VARCHAR(64) NOT NULL,
    lease_until BIGINT NOT NULL,
    last_code VARCHAR(64) NOT NULL,
    PRIMARY KEY (event_id, destination),
    FOREIGN KEY (event_id) REFERENCES event_records(event_id)
);
CREATE INDEX event_deliveries_ready ON event_deliveries(state, next_attempt, lease_until);
-- +down
DROP TABLE event_deliveries;
DROP TABLE event_records;
