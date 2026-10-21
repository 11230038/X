-- +goose Up
-- The mastery path gets its own conversation surface. A mastery turn already
-- settles which path it is about when it opens (`mastery.Open`), but the
-- session it runs in was a plain chat until now: it shared the chat page's
-- list, and a learner who opened `/mastery` had no list of the conversations
-- that had worked on a path.
--
-- `kind` is the same partition it was in migration 00022, widened by one value.
-- It is still not a permission: a mastery conversation runs the `mastery_path`
-- capability behind the chat socket, and which list it appears in is the whole
-- of what this column decides.
ALTER TABLE sessions DROP CHECK sessions_kind_state;

-- The same closed set the Go type enforces.
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_state CHECK (kind IN ('chat', 'writing', 'mastery'));

-- +goose Down
-- Rows written by a mastery conversation have no home in the two-value schema,
-- and the constraint cannot be narrowed while they exist. They are folded back
-- into chat rather than deleted: the messages stay readable, they simply merge
-- into the chat list again.
UPDATE sessions SET kind = 'chat' WHERE kind = 'mastery';

ALTER TABLE sessions DROP CHECK sessions_kind_state;
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_state CHECK (kind IN ('chat', 'writing'));
