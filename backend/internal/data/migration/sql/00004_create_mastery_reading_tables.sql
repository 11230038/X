-- +goose Up
CREATE TABLE mastery_paths (
    mastery_path_id TEXT PRIMARY KEY,
    owner_session_id TEXT NOT NULL,
    state_json JSON,
    version INTEGER,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT mastery_paths_id_not_blank CHECK (btrim(mastery_path_id) <> ''),
    CONSTRAINT mastery_paths_owner_session_not_blank CHECK (btrim(owner_session_id) <> ''),
    CONSTRAINT mastery_paths_version_positive CHECK (
        version IS NULL OR version >= 1
    ),
    CONSTRAINT mastery_paths_owner_session_fk FOREIGN KEY (owner_session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_paths_owner_creat
    ON mastery_paths (owner_session_id, creat_time DESC);
CREATE INDEX idx_mastery_paths_update_time
    ON mastery_paths (update_time DESC);

CREATE TABLE mastery_learning_evidences (
    mle_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    path_id TEXT NOT NULL,
    extra JSON,
    result TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source TEXT,
    quality TEXT,
    assessment_type TEXT,
    CONSTRAINT mastery_learning_evidences_id_not_blank CHECK (btrim(mle_id) <> ''),
    CONSTRAINT mastery_learning_evidences_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT mastery_learning_evidences_turn_not_blank CHECK (btrim(turn_id) <> ''),
    CONSTRAINT mastery_learning_evidences_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_learning_evidences_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT mastery_learning_evidences_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE,
    CONSTRAINT mastery_learning_evidences_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_evidences_path_creat
    ON mastery_learning_evidences (path_id, creat_time DESC);
CREATE INDEX idx_mastery_evidences_session
    ON mastery_learning_evidences (session_id);
CREATE INDEX idx_mastery_evidences_turn
    ON mastery_learning_evidences (turn_id);

CREATE TABLE mastery_path_leases (
    path_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT mastery_path_leases_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_path_leases_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT mastery_path_leases_turn_not_blank CHECK (btrim(turn_id) <> ''),
    CONSTRAINT mastery_path_leases_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE,
    CONSTRAINT mastery_path_leases_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT mastery_path_leases_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_path_leases_session
    ON mastery_path_leases (session_id);
CREATE INDEX idx_mastery_path_leases_turn
    ON mastery_path_leases (turn_id);

CREATE TABLE mastery_interactions (
    interaction_id TEXT PRIMARY KEY,
    path_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    user_answer TEXT,
    result_json JSON,
    question_json JSON,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT mastery_interactions_id_not_blank CHECK (btrim(interaction_id) <> ''),
    CONSTRAINT mastery_interactions_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_interactions_turn_not_blank CHECK (btrim(turn_id) <> ''),
    CONSTRAINT mastery_interactions_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT mastery_interactions_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE,
    CONSTRAINT mastery_interactions_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE,
    CONSTRAINT mastery_interactions_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_interactions_path_creat
    ON mastery_interactions (path_id, creat_time DESC);
CREATE INDEX idx_mastery_interactions_turn
    ON mastery_interactions (turn_id);
CREATE INDEX idx_mastery_interactions_session
    ON mastery_interactions (session_id);

CREATE TABLE mastery_events (
    mastery_events_id TEXT PRIMARY KEY,
    path_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    version INTEGER,
    payload_json JSON,
    event_type TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT mastery_events_id_not_blank CHECK (btrim(mastery_events_id) <> ''),
    CONSTRAINT mastery_events_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_events_turn_not_blank CHECK (btrim(turn_id) <> ''),
    CONSTRAINT mastery_events_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT mastery_events_version_positive CHECK (
        version IS NULL OR version >= 1
    ),
    CONSTRAINT mastery_events_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE,
    CONSTRAINT mastery_events_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE,
    CONSTRAINT mastery_events_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_events_path_version
    ON mastery_events (path_id, version, creat_time DESC);
CREATE INDEX idx_mastery_events_turn
    ON mastery_events (turn_id);
CREATE INDEX idx_mastery_events_session
    ON mastery_events (session_id);

CREATE TABLE mastery_path_sessions (
    path_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_time TIMESTAMPTZ,
    CONSTRAINT mastery_path_sessions_pk PRIMARY KEY (path_id, session_id),
    CONSTRAINT mastery_path_sessions_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_path_sessions_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT mastery_path_sessions_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE,
    CONSTRAINT mastery_path_sessions_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_mastery_path_sessions_session_seen
    ON mastery_path_sessions (session_id, last_seen_time DESC, path_id);

CREATE TABLE mastery_topic_meta (
    path_id TEXT PRIMARY KEY,
    goal TEXT,
    description TEXT,
    emoji TEXT,
    status TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT mastery_topic_meta_path_not_blank CHECK (btrim(path_id) <> ''),
    CONSTRAINT mastery_topic_meta_path_fk FOREIGN KEY (path_id)
        REFERENCES mastery_paths (mastery_path_id) ON DELETE CASCADE
);

CREATE TABLE reading_materials (
    material_id TEXT PRIMARY KEY,
    content_id TEXT,
    filename TEXT,
    source_type TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT reading_materials_id_not_blank CHECK (btrim(material_id) <> '')
);

CREATE INDEX idx_reading_materials_content
    ON reading_materials (content_id)
    WHERE content_id IS NOT NULL;
CREATE INDEX idx_reading_materials_update_time
    ON reading_materials (update_time DESC);
CREATE INDEX idx_reading_materials_source_type
    ON reading_materials (source_type)
    WHERE source_type IS NOT NULL;

CREATE TABLE reading_workspaces (
    workspace_id TEXT PRIMARY KEY,
    active_material_id TEXT,
    title TEXT,
    description TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT reading_workspaces_id_not_blank CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT reading_workspaces_active_material_fk FOREIGN KEY (active_material_id)
        REFERENCES reading_materials (material_id) ON DELETE SET NULL
);

CREATE INDEX idx_reading_workspaces_active_material
    ON reading_workspaces (active_material_id);
CREATE INDEX idx_reading_workspaces_update_time
    ON reading_workspaces (update_time DESC);

CREATE TABLE reading_workspace_sessions (
    workspace_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    active_material_id TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    CONSTRAINT reading_workspace_sessions_pk PRIMARY KEY (workspace_id, session_id),
    CONSTRAINT reading_workspace_sessions_workspace_not_blank CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT reading_workspace_sessions_session_not_blank CHECK (btrim(session_id) <> ''),
    CONSTRAINT reading_workspace_sessions_workspace_fk FOREIGN KEY (workspace_id)
        REFERENCES reading_workspaces (workspace_id) ON DELETE CASCADE,
    CONSTRAINT reading_workspace_sessions_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT reading_workspace_sessions_active_material_fk FOREIGN KEY (active_material_id)
        REFERENCES reading_materials (material_id) ON DELETE SET NULL
);

CREATE INDEX idx_reading_workspace_sessions_session
    ON reading_workspace_sessions (session_id);
CREATE INDEX idx_reading_workspace_sessions_active_material
    ON reading_workspace_sessions (active_material_id);
CREATE INDEX idx_reading_workspace_sessions_workspace_update
    ON reading_workspace_sessions (workspace_id, update_time DESC);

CREATE TABLE reading_workspace_materials (
    workspace_id TEXT NOT NULL,
    material_id TEXT NOT NULL,
    tab_order INTEGER,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_workspace_materials_pk PRIMARY KEY (workspace_id, material_id),
    CONSTRAINT reading_workspace_materials_workspace_not_blank CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT reading_workspace_materials_material_not_blank CHECK (btrim(material_id) <> ''),
    CONSTRAINT reading_workspace_materials_tab_order_nonnegative CHECK (
        tab_order IS NULL OR tab_order >= 0
    ),
    CONSTRAINT reading_workspace_materials_workspace_fk FOREIGN KEY (workspace_id)
        REFERENCES reading_workspaces (workspace_id) ON DELETE CASCADE,
    CONSTRAINT reading_workspace_materials_material_fk FOREIGN KEY (material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE
);

CREATE INDEX idx_reading_workspace_materials_material
    ON reading_workspace_materials (material_id, workspace_id);

-- +goose Down
DROP TABLE reading_workspace_sessions;
DROP TABLE reading_workspace_materials;
DROP TABLE reading_workspaces;
DROP TABLE reading_materials;
DROP TABLE mastery_topic_meta;
DROP TABLE mastery_path_sessions;
DROP TABLE mastery_events;
DROP TABLE mastery_interactions;
DROP TABLE mastery_path_leases;
DROP TABLE mastery_learning_evidences;
DROP TABLE mastery_paths;
