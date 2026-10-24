-- +up
CREATE TABLE IF NOT EXISTS dep_profile_choices (
    account TEXT NOT NULL PRIMARY KEY,
    targets BLOB NULL,
    revision BIGINT NOT NULL DEFAULT 0
);

-- +down
DROP TABLE dep_profile_choices;
