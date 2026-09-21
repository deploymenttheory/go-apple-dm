-- +up
CREATE TABLE dep_account_locks (
    account TEXT NOT NULL PRIMARY KEY
);

-- +down
DROP TABLE dep_account_locks;
