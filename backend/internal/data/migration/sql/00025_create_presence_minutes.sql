-- +goose Up
-- Being here, written down one minute at a time.
--
-- The home page shows two numbers about attendance — how long today has been
-- spent in the app and how many days in a row it has been opened — and nothing
-- in the schema could answer either: `mastery_path_sessions.last_seen_time`
-- moves once per path, `sessions` only moves for a conversation, and
-- `llm_calls` only exists when a model was asked something. All three are
-- records of work, and neither number is about work; they are about the app
-- having been open.
--
-- One row per minute rather than a daily counter with a timer: a counter has to
-- be told when a visit ends, and a tab that is closed is never heard from
-- again — the row would keep counting. A minute is written when it is lived,
-- the count is a COUNT over the day, and a process that dies in the middle of
-- a visit loses at most the minute it died in.
--
-- Rows are bounded by the clock: at most 1440 per account per day. Nothing
-- prunes them — the streak walks back over days, and "the days this account
-- was here" is a fact that does not stop being true. If the table ever needs
-- a retention window, that is a deployment number, not something to guess
-- here.
--
-- The minute is a timestamp and not a counter on `users`: a row keyed by
-- (owner, minute) is idempotent, so a browser that beats twice in one minute
-- and a client that retries a lost request both write the same row, and the
-- count cannot double.
CREATE TABLE presence_minutes (
    owner_id BIGINT NOT NULL,
    minute_at DATETIME NOT NULL,
    PRIMARY KEY (owner_id, minute_at),
    CONSTRAINT presence_minutes_owner_fk FOREIGN KEY (owner_id)
        REFERENCES users (user_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE presence_minutes;
