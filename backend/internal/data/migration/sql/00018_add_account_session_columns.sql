-- +goose Up
-- The account gains the four columns that answer "may this still be used", and
-- this is the first time users has been altered since 00001 built it — 00014
-- touched the table only to add a check constraint.
--
-- Two decisions live in here, each with the moment it has to be compared
-- against: one revocation, which takes back every token an account has, and one
-- lock, which stops it logging in again. Until now the only way to withdraw a
-- credential was the disable flag, which is all or nothing and belongs to an
-- administrator rather than to the account itself.
--
-- There is no index on any of the four, and that is what lets them be added to
-- a live table cheaply: they are read from the single row a request already
-- reads, by primary key or by the unique username key, and nothing asks "which
-- accounts are locked". When something does, the index that serves that question
-- arrives with it — the same reasoning 00011 used for the columns it left
-- unindexed, except that here the query does not exist yet at all.

-- Revocation is a moment, not a list: every token signed at or before it stops
-- counting. NULL means "no revocation has ever happened", which is not the same
-- as an early moment — the judgement reads the difference between NULL and a
-- value — so the column is given no default to fall back on.
--
-- The value is written by Go rather than by UTC_TIMESTAMP() in the statement
-- that writes it, and that is not a detail. What it is compared against is the
-- token's own iat, which comes from the application's clock; a database clock a
-- second off from that one would let a token signed just before the revocation
-- look newer than it, and the rule would quietly refuse to fire. One clock, used
-- in two places.
ALTER TABLE users ADD COLUMN token_valid_after DATETIME NULL;

-- Consecutive failed logins. Reaching the threshold writes locked_until and
-- clears this pair, so this counts a run and not a history.
ALTER TABLE users ADD COLUMN failed_attempts INT NOT NULL DEFAULT 0;

-- When the run that failed_attempts is counting began. A failure arriving after
-- this moment plus the window has passed starts a new run at 1 instead of adding
-- to a count that is older than the window it belongs to. There is deliberately
-- no column for "when the last failure was": the only question this version asks
-- is when the current run started.
ALTER TABLE users ADD COLUMN failed_window_start DATETIME NULL;

-- NULL is "not locked". A moment already in the past means the same thing,
-- because the judgement is "locked_until is later than now" — so an expired lock
-- needs no writer to come along and clear it, and a deployment that was down
-- through the expiry lets the account straight back in.
ALTER TABLE users ADD COLUMN locked_until DATETIME NULL;

-- +goose Down
-- The order is the reverse of the up side; the four columns do not depend on
-- each other, so it is a formality. One consequence is not, and it is worth
-- saying out loud even in a repository that does not promise backwards
-- compatibility: dropping token_valid_after resurrects every token that was
-- revoked before the rollback. Credentials coming back to life is a different
-- kind of loss from a column going away.
ALTER TABLE users DROP COLUMN locked_until;
ALTER TABLE users DROP COLUMN failed_window_start;
ALTER TABLE users DROP COLUMN failed_attempts;
ALTER TABLE users DROP COLUMN token_valid_after;
