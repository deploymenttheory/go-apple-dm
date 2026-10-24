-- +up
CREATE TABLE IF NOT EXISTS dep_profile_choices (
    account VARCHAR(255) NOT NULL PRIMARY KEY,
    targets LONGBLOB NULL,
    revision BIGINT NOT NULL DEFAULT 0
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;

-- +down
DROP TABLE dep_profile_choices;
