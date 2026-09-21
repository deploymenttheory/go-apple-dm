-- +up
CREATE TABLE dep_account_locks (
    account VARCHAR(255) NOT NULL PRIMARY KEY
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE dep_account_locks;
