-- +up
CREATE TABLE dep_account_locks (
    account VARCHAR(255) NOT NULL PRIMARY KEY
);

-- +down
DROP TABLE dep_account_locks;
