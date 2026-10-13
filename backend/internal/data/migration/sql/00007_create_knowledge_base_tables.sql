-- +goose Up
-- Materials, parsing and retrieval (V2). Three tables, one idea: a file becomes
-- a document, a document becomes chunks, and the chunks are what retrieval
-- reads. Nothing foreign to seekdb is used to hold it together — no queue table,
-- no external index. The queue for embedding work IS one predicate over
-- documents ("not yet ready"), and the search index IS two index definitions on
-- document_chunks.
--
-- seekdb notes, all four verified by probe before this file was written:
--   * A single CREATE TABLE may carry both a FULLTEXT and a VECTOR index; both
--     appear in SHOW INDEX as their own Index_type.
--   * FULLTEXT ... WITH PARSER ngram builds on MEDIUMTEXT. The parser defaults
--     to ngram_token_size=2, so a one-character query matches nothing; callers
--     must reject those.
--   * The vector dimension is enforced by the column itself: inserting a
--     3-element value into VECTOR(1024) fails with "inconsistent dimension".
--     The startup check in §3 of the design is therefore backed by the
--     database, not only by our own configuration comparison.
--   * TEXT is neither indexable nor defaultable, so keyed columns stay
--     VARCHAR(n); extracted_text is MEDIUMTEXT because ATT-03 allows 200k
--     characters and TEXT would cap at 64KB.
--
-- One shape is forbidden here, and it cost a day to find. In this seekdb build,
-- a table that carries a VECTOR index must not have an index on a column that is
-- also a foreign key: deleting a row from the referenced parent then fails with
-- "ERROR 4016 Internal error", whether or not the child still has rows, whatever
-- the foreign key's own ON DELETE action is, and whatever sort of index it is.
-- The probes that pinned it down:
--
--   child VECTOR index + no index on the FK column   -> delete parent works
--   child VECTOR index + INDEX  (fk_col)             -> ERROR 4016
--   child VECTOR index + UNIQUE (fk_col)             -> ERROR 4016
--   child VECTOR index + INDEX  (fk_col, other)      -> ERROR 4016
--   child VECTOR index + INDEX  (other, fk_col)      -> delete parent works
--   child FULLTEXT index + INDEX (fk_col, other)     -> delete parent works
--   child no vector index + INDEX (fk_col, other)    -> delete parent works
--
-- So the vector index is what makes it fatal, and the FK column merely sitting
-- in an index is enough. Restating it as a rule for this file: no index may be
-- declared on documents.kb_id, documents.file_id, or
-- document_chunks.document_id. seekdb creates none by itself, which is why the
-- tables below work; adding one back is how to see the failure.
--
-- The cost of obeying it is that nothing indexes document_chunks.document_id,
-- so reading one document's chunks scans the table. The embedding worker, which
-- does that most often, can instead range-scan the primary key, because
-- chunk_id starts with the document id (see below).
--
-- Chunk identity is carried by chunk_id rather than by a UNIQUE key on
-- (document_id, revision, ordinal): that key was the first shape to trip over
-- this, and it says the same thing only while the ordinal is part of the id.
CREATE TABLE knowledge_bases (
    kb_id VARCHAR(128) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    provider VARCHAR(64) NOT NULL,
    -- Which model produced the vectors already stored for this base. Startup
    -- refuses to run when this disagrees with EMBEDDING_MODEL: mixing two
    -- models' vectors in one column would compare distances that mean nothing.
    embedding_model VARCHAR(128) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT knowledge_bases_id_not_blank CHECK (TRIM(kb_id) <> ''),
    CONSTRAINT knowledge_bases_name_not_blank CHECK (TRIM(name) <> ''),
    CONSTRAINT knowledge_bases_provider_supported CHECK (provider IN ('native')),
    CONSTRAINT knowledge_bases_embedding_model_not_blank CHECK (TRIM(embedding_model) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- No partial index, so "exactly one default base" cannot be a unique key and is
-- kept by the service inside one transaction instead.
CREATE INDEX idx_knowledge_bases_update_time ON knowledge_bases (update_time DESC);

CREATE TABLE documents (
    document_id VARCHAR(128) PRIMARY KEY,
    -- NULL means this document exists only as an attachment of a turn. It is the
    -- same row type either way: an attachment and a knowledge base entry differ
    -- in whether anyone may retrieve from them later, not in what they are.
    kb_id VARCHAR(128) NULL,
    file_id VARCHAR(128) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    -- Which parser produced extracted_text; kept so a later re-parse with a
    -- different engine is visible rather than silent.
    engine VARCHAR(64) NOT NULL,
    extracted_text MEDIUMTEXT,
    text_chars BIGINT NOT NULL DEFAULT 0,
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    -- The settings this document was chunked with. Recorded per document so a
    -- rebuild after a configuration change is reproducible after the fact.
    chunk_size INT NOT NULL,
    chunk_overlap INT NOT NULL,
    -- Chunks are written under revision n, then this pointer moves n-1 -> n in
    -- one statement. Before it moves the new chunks are invisible; after it
    -- moves the old ones are. There is no window where a query sees both or
    -- neither. This is the same shape as turns.state_version in V1.
    active_revision INT NOT NULL DEFAULT 1,
    -- pending / running / ready / failed. For the UI and for diagnosis only:
    -- retrieval correctness depends on whether a chunk has a vector, never on
    -- this column.
    vector_state VARCHAR(32) NOT NULL DEFAULT 'pending',
    vector_owner VARCHAR(128) NULL,
    vector_claimed_at DATETIME NULL,
    vector_error VARCHAR(128) NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT documents_id_not_blank CHECK (TRIM(document_id) <> ''),
    CONSTRAINT documents_file_not_blank CHECK (TRIM(file_id) <> ''),
    CONSTRAINT documents_filename_not_blank CHECK (TRIM(filename) <> ''),
    CONSTRAINT documents_engine_not_blank CHECK (TRIM(engine) <> ''),
    CONSTRAINT documents_text_chars_nonnegative CHECK (text_chars >= 0),
    CONSTRAINT documents_chunk_size_positive CHECK (chunk_size > 0),
    CONSTRAINT documents_chunk_overlap_valid CHECK (chunk_overlap >= 0 AND chunk_overlap < chunk_size),
    CONSTRAINT documents_active_revision_positive CHECK (active_revision >= 1),
    CONSTRAINT documents_vector_state_supported CHECK (
        vector_state IN ('pending', 'running', 'ready', 'failed')
    ),
    -- A claimed document has an owner and a timestamp; anything else has
    -- neither. Without this, a row stuck in 'running' with a NULL claim is
    -- invisible to the lease and never reclaimed.
    CONSTRAINT documents_vector_claim_coherent CHECK (
        (vector_state = 'running' AND vector_owner IS NOT NULL AND vector_claimed_at IS NOT NULL)
        OR (vector_state <> 'running' AND vector_owner IS NULL AND vector_claimed_at IS NULL)
    ),
    CONSTRAINT documents_kb_fk FOREIGN KEY (kb_id)
        REFERENCES knowledge_bases (kb_id) ON DELETE CASCADE,
    CONSTRAINT documents_file_fk FOREIGN KEY (file_id)
        REFERENCES library_files (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- The embedding queue. A worker claims a document that is not ready and whose
-- claim is absent or older than the lease, which is why the state leads the key.
--
-- Neither kb_id nor file_id is indexed, for the reason in the header. Listing a
-- base's material therefore scans documents and sorts; a base holds few enough
-- documents for that to be the right trade while the alternative is a database
-- error.
CREATE INDEX idx_documents_vector_state
    ON documents (vector_state, vector_claimed_at);

CREATE TABLE document_chunks (
    -- Deterministic: chk_<document_id>_<revision>_<ordinal>. The primary key is
    -- then also the guarantee that one document revision cannot hold two chunks
    -- at the same ordinal, which is what a unique key on those three columns
    -- would have said — and that key is the shape this build cannot delete
    -- through (see the header). Writing a revision twice is caught here instead
    -- of silently doubling a document.
    chunk_id VARCHAR(128) PRIMARY KEY,
    document_id VARCHAR(128) NOT NULL,
    -- Denormalised from documents on purpose. Both the vector index and the
    -- scalar filter then sit in one table, so a filtered top-k stays a
    -- single-table scan: seekdb plans it as VECTOR INDEX ADAPTIVE SCAN
    -- (PRE-FILTER), filter first and HNSW afterwards. Joining documents first
    -- and running ANN afterwards would cost more and be harder to reason about.
    kb_id VARCHAR(128) NULL,
    revision INT NOT NULL,
    -- revision = active corresponds to active only after the pointer moves, so
    -- this is flipped by the same transaction that moves it.
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    ordinal INT NOT NULL,
    content MEDIUMTEXT NOT NULL,
    -- Offsets into documents.extracted_text. Together with file_id these are the
    -- coordinates a citation is opened with (AC-06), and they are generated
    -- here rather than by the model, which is what makes AIQ-02 hold.
    char_start BIGINT NOT NULL,
    char_end BIGINT NOT NULL,
    page INT NULL,
    -- NULL until the embedding worker fills it. Retrieval does not wait: the
    -- full-text branch is usable from the moment chunks are written, and RRF
    -- degrades to one branch when the second is missing.
    embedding VECTOR(1024) NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT document_chunks_id_not_blank CHECK (TRIM(chunk_id) <> ''),
    CONSTRAINT document_chunks_revision_positive CHECK (revision >= 1),
    CONSTRAINT document_chunks_ordinal_nonnegative CHECK (ordinal >= 0),
    CONSTRAINT document_chunks_span_valid CHECK (char_end > char_start),
    CONSTRAINT document_chunks_document_fk FOREIGN KEY (document_id)
        REFERENCES documents (document_id) ON DELETE CASCADE,
    FULLTEXT INDEX document_chunks_content_ft (content) WITH PARSER ngram,
    VECTOR INDEX document_chunks_embedding_vec (embedding)
        WITH (DISTANCE=COSINE, TYPE=HNSW, M=16, EF_CONSTRUCTION=200, LIB=VSAG, EF_SEARCH=64)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_document_chunks_kb_active
    ON document_chunks (kb_id, is_active, ordinal);

-- +goose Down
DROP TABLE document_chunks;
DROP TABLE documents;
DROP TABLE knowledge_bases;
