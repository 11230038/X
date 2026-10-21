-- +goose Up
-- A session now says which door it belongs to: the chat page or the writing
-- page. The two surfaces list the same table, and without a stored answer to
-- "which one is this" each would have to filter by something it guessed — a
-- title prefix, a preference, or a second table that would be joined on every
-- list read.
--
-- The default is not a placeholder: every row written before this migration is
-- a chat session, because writing was not a place a session could be started
-- from. The column therefore backfills itself through its default and nothing
-- has to be rewritten.
--
-- This is a partition, not a permission surface. A writing session is still a
-- conversation: it runs the chat capability and is guarded by the chat surface.
-- The column only decides which list a row appears in.
ALTER TABLE sessions ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'chat';

-- The same closed set the Go type enforces. A check is worth the ALTER because
-- the value is read by a list filter: a typo written by some future caller
-- would not fail loudly, it would simply make the row disappear from both
-- lists.
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_state CHECK (kind IN ('chat', 'writing'));

-- The listing asks for one kind, one state and one order. The owner column is
-- not in this index: an administrator lists everybody's rows and would not be
-- served by a leading owner column, while a person's own rows are few enough
-- that the kind/state/order prefix is the part that matters.
CREATE INDEX idx_sessions_kind_active_update ON sessions (kind, is_deleted, update_time DESC);

-- +goose Down
-- Dropping the column merges the two lists again. The rows themselves survive;
-- what goes away is the only record of which list they belonged to, so a
-- rolled-back deployment cannot tell them apart afterwards.
ALTER TABLE sessions DROP INDEX idx_sessions_kind_active_update;
ALTER TABLE sessions DROP CHECK sessions_kind_state;
ALTER TABLE sessions DROP COLUMN kind;
