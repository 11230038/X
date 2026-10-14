-- +goose Up
-- One column and one index, on a table that has been carrying rows since V3's
-- practice loop went live. This is V3 §8 第 7 条's leftover: the judgement of a
-- review has always been written, but it has only ever been written into
-- outcome_json, where nothing can index it.
--
-- Two consequences of that shape are what make this worth a column at all:
--
--   * "how many of this question's reviews were right" is a range on one index
--     once the answer is a column, and a decode of every row in the window
--     until then;
--   * the judgement is a three-valued thing and JSON cannot say so. A skipped
--     question has no judgement — V3 §0.3 第 10 条 settled that `judgment` is
--     null exactly when the user skipped — and inside a JSON document a missing
--     key and a null one are the same read. Out here a NULL column is one
--     meaning and TRUE / FALSE are the other two, and the two do not collide.

-- The column is nullable and stays nullable. NOT NULL DEFAULT FALSE would have
-- been the tidier-looking schema and the wrong one: it turns every historical
-- skip into a wrong answer, and a question nobody answered is not a question
-- somebody got wrong.
ALTER TABLE practice_review_events
    ADD COLUMN is_correct BOOLEAN NULL;

-- Backfill, before the index rather than after it: the same order 00008 and
-- 00009 both took, and for the same reason. Rows a deployment wrote are in this
-- table and the index is built over whatever is in it; put the index up first and
-- there is a window where the column it indexes is empty.
--
-- JSON_UNQUOTE(JSON_EXTRACT(...)) and not `->>`: this engine does not parse the
-- shorthand at all (V3 §0.3 第 11 条 learned that the expensive way). The
-- comparison is against the string 'true' because a JSON boolean read out of a
-- document is the literal true, and JSON_UNQUOTE is what turns that literal into
-- the text 'true'.
--
-- The WHERE clause is the whole safety of this statement. It is what leaves a
-- skipped review's is_correct NULL, which is the one outcome that must not be
-- got wrong here: backfill it and "unanswered" becomes indistinguishable from
-- "answered wrong" for every row that already exists, permanently and silently.
UPDATE practice_review_events
SET is_correct = (JSON_UNQUOTE(JSON_EXTRACT(outcome_json, '$.judgment.is_correct')) = 'true')
WHERE JSON_EXTRACT(outcome_json, '$.judgment.is_correct') IS NOT NULL;

-- The column first in the key, because it is the one that is always known: a
-- caller asking about a question's correctness knows the question and not the
-- answer. review_time DESC is the shape of every read of this table — "the most
-- recent reviews of this question" — which is what makes the window a prefix of
-- the index rather than a sort.
CREATE INDEX idx_practice_review_events_entry_outcome
    ON practice_review_events (notebook_entries_id, is_correct, review_time DESC);

-- +goose Down
-- Index first, because it names a column that has to outlive it: dropping the
-- column underneath a standing index is the same wall 00009 and 00010 both
-- documented, met from the other side.
DROP INDEX idx_practice_review_events_entry_outcome ON practice_review_events;
ALTER TABLE practice_review_events DROP COLUMN is_correct;
