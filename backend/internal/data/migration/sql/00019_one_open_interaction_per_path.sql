-- +goose Up
-- One open interaction per mastery path, moved out of the application and into
-- the schema. This is the second time a rule has been moved here, and it is the
-- same shape as the first (00009) for the same reason.
--
-- Ask reads the path's interactions inside its transaction, finds no open one
-- and inserts. That read is meant to be what stops the second writer, and on
-- this engine it is not: READ-COMMITTED takes record locks and no gap locks, so
-- a read matching no row locks nothing at all — and "this path has no open
-- interaction" is exactly that empty range. 00009 measured the hole on turns
-- with sixteen asks over four sessions and found four active turns left behind.
-- What holds where a read does not is a unique index, and a unique index needs
-- a column that is NULL for every row the rule is not about: the open ones take
-- the path and the answered ones take NULL, because a unique index admits any
-- number of NULLs and exactly one of each value.

-- Repair first, because the index cannot be built over rows that already break
-- the rule, and a deployment that ran the old code has them: two concurrent asks
-- both read nothing and both inserted.
--
-- What the repair deletes is a question that was put to the user and never
-- answered. The lowest interaction_id keeps the path — ids are generated in
-- arrival order, and 00009's "keep what the readers were already being shown"
-- instinct points the same way — and the later rows go. They cannot be marked
-- instead of deleted the way 00009 marked its extra turns: a turn has a status
-- to write and an open interaction has nowhere to say "this one is void".
-- result_json is what an answer is written into, so marking it would mean
-- inventing an answer, and inventing data is worse than deleting a row that
-- holds nothing but the fact that a question was asked.
--
-- Expected to delete 0 rows: no correct write path produces a duplicate, so the
-- only rows this removes are ones the server wrote while two callers raced.
--
-- Deleting from a table the subquery reads is refused (ERROR 1093), so the inner
-- select is wrapped in a derived table. That is 00009's spelling of the same
-- repair and the one this engine is known to parse; the multi-table DELETE form
-- that MySQL also allows was not taken, because this engine refuses MySQL
-- spellings it does not implement (V3 §0.3 第 11 条, on `->>`).
DELETE FROM mastery_interactions
WHERE interaction_id IN (
    SELECT interaction_id FROM (
        SELECT doomed.interaction_id AS interaction_id
        FROM mastery_interactions doomed
        JOIN mastery_interactions keep
          ON keep.path_id = doomed.path_id
         AND keep.result_json IS NULL
         AND doomed.result_json IS NULL
         AND keep.interaction_id < doomed.interaction_id
    ) AS superseded
);

-- Open means result_json IS NULL, which is the predicate the application's read
-- already used and therefore the meaning the repair above removes rows by.
--
-- The generated column is the storage spelling of "the scope this row takes, if
-- it takes one", which is 00009's active_scope with a different key: a path here
-- where a session was there. VARCHAR(128) because it has to hold path_id, and
-- 00009's active_scope is that width for the same reason.
ALTER TABLE mastery_interactions
    ADD COLUMN open_scope VARCHAR(128)
        GENERATED ALWAYS AS (IF(result_json IS NULL, path_id, NULL)) STORED,
    ADD UNIQUE KEY mastery_interactions_one_open (open_scope);

-- +goose Down
-- Two statements rather than one: this build refuses an index drop in the same
-- statement as a column change ("table redefinition and drop index in single
-- statement not supported"), which 00009's down side documents from the same
-- wall. Adding the two together in the up side is accepted.
ALTER TABLE mastery_interactions DROP INDEX mastery_interactions_one_open;
ALTER TABLE mastery_interactions DROP COLUMN open_scope;
