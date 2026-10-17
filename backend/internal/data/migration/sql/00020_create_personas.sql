-- +goose Up
-- A persona is a prompt an account saved for itself: a name, a one-line
-- description, and the instruction a turn should be run under.
--
-- It is not a role and carries no permission. Nothing here is read by the
-- authorization layer, and a turn that names a persona is still checked against
-- the account that started it. What a persona changes is what the model is
-- told, and only for the turn that named it.
--
-- The instruction is a row rather than a column on a session because it belongs
-- to one turn at a time. A conversation that switched personas halfway through
-- has not stored two personalities in one history: the messages stay what was
-- said, and the prompt is supplied again each time. That is also why there is no
-- foreign key from any conversation table to this one.
CREATE TABLE personas (
    persona_id  VARCHAR(128) PRIMARY KEY,
    owner_id    BIGINT NOT NULL,
    name        VARCHAR(64) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    content     TEXT NOT NULL,
    creat_time  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Deleting an account takes its personas with it. A persona that outlived
    -- its owner would be a prompt with no one to answer for it.
    CONSTRAINT personas_owner_fk FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE,
    CONSTRAINT personas_name_not_blank CHECK (TRIM(name) <> ''),
    CONSTRAINT personas_content_not_blank CHECK (TRIM(content) <> ''),
    -- One name per account: the selector in the composer shows names, and two
    -- rows with the same name would be two rows nobody could tell apart.
    CONSTRAINT personas_owner_name_unique UNIQUE (owner_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- +goose Down
DROP TABLE personas;
