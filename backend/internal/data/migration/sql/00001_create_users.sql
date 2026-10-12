-- +goose Up
-- seekdb is MySQL-compatible, so this file uses MySQL syntax. Two differences
-- from the previous PostgreSQL definitions are deliberate and affect behaviour:
--   * TEXT cannot be indexed and cannot carry a DEFAULT, so every keyed column
--     became VARCHAR(n) and the JSON columns lost their '{}' default (callers
--     always supply a value).
--   * utf8mb4_bin keeps comparisons byte-exact. seekdb defaults to
--     utf8mb4_general_ci, which would treat 'Test' and 'test' as one username.
CREATE TABLE users (
    user_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(64) NOT NULL,
    password VARCHAR(255) NOT NULL,
    disable BOOLEAN NOT NULL DEFAULT FALSE,
    avatar VARCHAR(255) NOT NULL DEFAULT '',
    role VARCHAR(64) NOT NULL DEFAULT 'user',
    preset VARCHAR(64) NOT NULL DEFAULT '',
    learner_profile JSON NOT NULL,
    extra JSON NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_username_unique UNIQUE (username),
    CONSTRAINT users_username_not_blank CHECK (TRIM(username) <> ''),
    CONSTRAINT users_password_not_blank CHECK (TRIM(password) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- +goose Down
DROP TABLE users;
