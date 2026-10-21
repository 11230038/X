-- +goose Up
-- The question bank gets a conversation page of its own: a place to ask about a
-- question that is already in the bank, rather than a general chat that happens
-- to mention one. The page is under the bank's title, and its history belongs
-- there — the chat page should not fill up with conversations about individual
-- questions.
--
-- `kind` is the same partition migration 00022 introduced, widened by one value.
-- It is still not a permission: these conversations run the ordinary `chat`
-- capability behind the chat socket, and this column only decides which list a
-- row appears in.
ALTER TABLE sessions DROP CHECK sessions_kind_state;

-- The same closed set the Go type enforces.
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_state CHECK (kind IN ('chat', 'writing', 'mastery', 'question_bank'));

-- +goose Down
-- Rows written by the question-bank conversations have no home in the three-value
-- schema, and the constraint cannot be narrowed while they exist. They are folded
-- back into chat rather than deleted: the messages stay readable and the
-- conversation stays reachable, it simply merges into the chat list again.
UPDATE sessions SET kind = 'chat' WHERE kind = 'question_bank';

ALTER TABLE sessions DROP CHECK sessions_kind_state;
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_state CHECK (kind IN ('chat', 'writing', 'mastery'));
