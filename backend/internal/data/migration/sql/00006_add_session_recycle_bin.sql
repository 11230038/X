-- +goose Up
-- The v2 sessions table has no way to express "in the recycle bin", so CHAT-06's
-- soft delete / restore / permanent delete triple cannot be represented. This
-- migration adds the two columns and the invariant that keeps them coherent.
--
-- seekdb has no partial indexes, so the live-row filter leads the index key
-- rather than sitting in a WHERE clause.
ALTER TABLE sessions
    ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN deleted_at DATETIME NULL;

ALTER TABLE sessions ADD CONSTRAINT sessions_deleted_state CHECK (
    (is_deleted = FALSE AND deleted_at IS NULL)
    OR (is_deleted = TRUE AND deleted_at IS NOT NULL)
);

CREATE INDEX idx_sessions_active_update ON sessions (is_deleted, update_time DESC);

-- +goose Down
ALTER TABLE sessions DROP INDEX idx_sessions_active_update;
ALTER TABLE sessions DROP CHECK sessions_deleted_state;
ALTER TABLE sessions DROP COLUMN is_deleted, DROP COLUMN deleted_at;
