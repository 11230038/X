-- +goose Up
-- A model call need not happen inside a conversation (V5 §2.7).
--
-- 00005 built this table on an assumption that held while every call was a
-- turn's: session_id and turn_id are NOT NULL and have to hold a name. §2.7
-- decorates the provider instead, so the table now records every call the
-- process makes — and two of them are made from an HTTP route with no
-- conversation anywhere above them: a review being judged, and a reading
-- extension being run.
--
-- The alternative was to write a placeholder into those columns — the empty
-- string, or a word like "-" — and it is refused for the reason the audit's
-- empty detail is stored as NULL: a made-up value and NULL read the same to
-- somebody looking at a row and differently to somebody counting rows. "This
-- call belongs to no conversation" is a fact about the call, and a column that
-- cannot hold it pushes the fact into the reader's head.
--
-- What stays NOT NULL is who paid for the call and where it came from. A call
-- with no account and no source is not a call without a conversation; it is a
-- call site that forgot to say, and the recorder refuses to make one (§2.7).
-- §2.9's quota is summed per account, so a row with no owner is a call nobody
-- was charged for — invisible in exactly the way a usage table must not be.

ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_session_not_blank;
ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_turn_not_blank;
ALTER TABLE llm_calls MODIFY COLUMN session_id VARCHAR(128) NULL;
ALTER TABLE llm_calls MODIFY COLUMN turn_id VARCHAR(128) NULL;

-- +goose Down
-- The rows the up side allows would not survive this: a rollback would have to
-- invent a conversation for each of them, and refusing is a better record than
-- a name nobody wrote.
ALTER TABLE llm_calls MODIFY COLUMN session_id VARCHAR(128) NOT NULL;
ALTER TABLE llm_calls MODIFY COLUMN turn_id VARCHAR(128) NOT NULL;
ALTER TABLE llm_calls ADD CONSTRAINT llm_calls_session_not_blank
    CHECK (session_id REGEXP '[^[:space:]]');
ALTER TABLE llm_calls ADD CONSTRAINT llm_calls_turn_not_blank
    CHECK (turn_id REGEXP '[^[:space:]]');
