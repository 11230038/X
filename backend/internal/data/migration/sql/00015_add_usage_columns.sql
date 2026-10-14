-- +goose Up
-- The numbers inside usage_json, promoted to columns (V5 §2.8).
--
-- This is the second time this has happened here — 00011 did it for
-- practice_review_events.correctness — and for the same reason: §17.1 aggregates
-- by model, by provider and by source, and a field inside a JSON document is not
-- something an index can answer. The cost is the same too: one value written in
-- two places can drift. The discipline is that both are written from one source
-- and only the column is ever read back.
--
-- usage_json stays. An upstream may return things no column holds — cache hits,
-- generation speed — and "what the columns do not carry is still in the
-- original" is better than "it was dropped".
--
-- There is no base_url column, and that absence is a decision rather than an
-- oversight: AC-13 asks for numeric usage with no prompt, response, credential
-- or endpoint, and the endpoint is where the request was sent.

ALTER TABLE llm_calls
    ADD COLUMN provider VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN model VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN prompt_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN completion_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN duration_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN ttft_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN finished_at DATETIME NULL,
    ADD COLUMN outcome VARCHAR(16) NOT NULL DEFAULT '';

-- outcome is the empty string while a call is open and one of three words once
-- it is not, and the vocabulary is closed here for the same reason
-- audit_events.result is: an outcome nobody can enumerate is one nothing can
-- count. Written with a check rather than left to the writer, because the writer
-- is the code this schema is there to disagree with.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_outcome_supported
        CHECK (outcome IN ('', 'ok', 'error', 'cancelled'));

-- finished_at IS NULL means "this call never came back". That is a state to be
-- able to query, not a row that is missing, and it is the whole reason the write
-- is in two parts (§2.7): a call that crashed and a call that is still holding
-- on are both visible, and neither has to be guessed at from a row that is not
-- there.
CREATE INDEX idx_llm_calls_open ON llm_calls (finished_at, started_at);

-- The quota's index: one account over one time window (§2.9).
CREATE INDEX idx_llm_calls_owner_started ON llm_calls (owner_id, started_at DESC);

-- The aggregate's index: by model, by source, over time.
CREATE INDEX idx_llm_calls_source_started ON llm_calls (source, started_at DESC);

-- +goose Down
DROP INDEX idx_llm_calls_source_started ON llm_calls;
DROP INDEX idx_llm_calls_owner_started ON llm_calls;
DROP INDEX idx_llm_calls_open ON llm_calls;

ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_outcome_supported;

ALTER TABLE llm_calls
    DROP COLUMN outcome,
    DROP COLUMN finished_at,
    DROP COLUMN ttft_ms,
    DROP COLUMN duration_ms,
    DROP COLUMN total_tokens,
    DROP COLUMN completion_tokens,
    DROP COLUMN prompt_tokens,
    DROP COLUMN model,
    DROP COLUMN provider;
