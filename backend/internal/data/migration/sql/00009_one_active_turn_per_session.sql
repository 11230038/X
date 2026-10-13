-- +goose Up
-- RT-02, moved into the schema: a session runs one turn at a time, and until
-- now that was the application's claim rather than something the database
-- enforced.
--
-- Start reads the session's turns with a locking read, finds none, and inserts.
-- The lock was meant to serialise the two: a second Start arriving in between
-- would wait and then see the first. It does not hold here. This engine runs
-- READ-COMMITTED, where a locking read takes record locks and no gap locks, so
-- a read that matches no row locks nothing at all — and that empty range is
-- exactly the case this rule is about. Sixteen asks over four sessions, all
-- released at once, left four active turns in three of them.
--
-- The first two files that added a rule like this (00008) are the same shape,
-- for the same reason: the mechanism is a generated column that is NULL for
-- every row the rule does not speak about, because a unique index admits any
-- number of NULLs and exactly one non-NULL value.
--
-- The list below is the statuses that are not terminal — the storage spelling
-- of Status.Active(), which is "valid and not terminal". Written as "not one of
-- the terminal ones" rather than "one of the active ones" on purpose: a status
-- added later that means an unfinished turn takes the session's slot without a
-- migration, and only a new terminal status needs one. Taking a slot that
-- should be free leaves a visible turn to end; freeing one that should be taken
-- is a second turn writing into one conversation.
--
-- Repair first, and not as decoration: a unique index cannot be built over data
-- that already breaks the rule. A deployment that ran the old code has the
-- duplicate turns, so the repair belongs here rather than in a note telling
-- somebody to clean up first.

-- Repair: of several unfinished turns in one session, the newest keeps the
-- slot.
--
-- The newest is the one ActiveTurn resolves to (`update_time DESC`), so a
-- deployment that ends up with one keeps the turn its own readers were already
-- being shown. A NULL update_time sorts last under DESC, and the join below
-- treats it the same way: such a row is never the newer one, and among rows
-- that are all NULL the id decides — the same tiebreak the adapter's order
-- gives.
--
-- The others are cancelled rather than failed. A failure carries a reason and a
-- code, and there is no reason to give: the turn did not fail, it was left
-- behind by a rule that was not being enforced. Cancelled says what a client
-- saw — a turn that stopped without an answer — and finish_time says when
-- whoever looked next ended it.
--
-- The self-join is wrapped in a derived table so MySQL materialises it; without
-- that it refuses to read the table it is updating (ERROR 1093).
UPDATE turns
SET status = 'cancelled',
    state_version = state_version + 1,
    finish_time = NOW(),
    update_time = NOW()
WHERE turn_id IN (
    SELECT turn_id FROM (
        SELECT loser.turn_id AS turn_id
        FROM turns loser
        JOIN turns newer
          ON newer.session_id = loser.session_id
         AND newer.status NOT IN ('completed', 'failed', 'cancelled')
         AND (newer.update_time > loser.update_time
              OR (newer.update_time <=> loser.update_time
                  AND newer.turn_id > loser.turn_id))
        WHERE loser.status NOT IN ('completed', 'failed', 'cancelled')
        GROUP BY loser.turn_id
    ) AS leftovers
);

-- One unfinished turn per session.
ALTER TABLE turns
    ADD COLUMN active_scope VARCHAR(128)
        GENERATED ALWAYS AS (
            IF(status NOT IN ('completed', 'failed', 'cancelled'), session_id, NULL)
        ) STORED,
    ADD UNIQUE KEY turns_one_active_per_session (active_scope);

-- +goose Down
-- Two statements rather than one: this build refuses an index drop in the same
-- statement as a column change ("table redefinition and drop index in single
-- statement not supported"), the same way 00008's Down is split. Adding the two
-- together in the Up is accepted.
ALTER TABLE turns DROP INDEX turns_one_active_per_session;
ALTER TABLE turns DROP COLUMN active_scope;
