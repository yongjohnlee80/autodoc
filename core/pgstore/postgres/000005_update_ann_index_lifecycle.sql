-- The approximate (HNSW) index of each embedding model, and the one builder that
-- makes them. Both tables are global, never per tenant: one index serves every
-- tenant on a model, and PostgreSQL builds one index on a table at a time.
--
-- One row per model's index. state is the lifecycle: wanted, then building,
-- then ready (or failed, retried with a backoff until attempts runs out), and
-- retiring once no tenant uses the model. def is the index's canonical
-- definition, read from the catalog when it became ready.
CREATE TABLE rag_ann_index (
    model    text        NOT NULL PRIMARY KEY,
    name     text        NOT NULL,
    dims     integer     NOT NULL,
    def      text        NOT NULL DEFAULT '',
    state    text        NOT NULL DEFAULT 'wanted'
             CHECK (state IN ('wanted', 'building', 'ready', 'failed', 'retiring')),
    attempts integer     NOT NULL DEFAULT 0,
    next_at  timestamptz,
    error    text        NOT NULL DEFAULT '',
    updated  timestamptz NOT NULL DEFAULT now()
);

-- The builder: one row for the table, so two servers wanting two models take
-- turns. model is the index being built (NULL when free); owner and generation
-- fence every write a builder makes; the backend is the session running the
-- build, marked so recovery can find, cancel or terminate it and nothing else.
CREATE TABLE rag_ann_builder (
    id            integer     NOT NULL PRIMARY KEY CHECK (id = 1),
    model         text,
    owner         text        NOT NULL DEFAULT '',
    generation    bigint      NOT NULL DEFAULT 0,
    backend_pid   integer,
    backend_start timestamptz,
    backend_token text        NOT NULL DEFAULT '',
    lease_until   timestamptz
);
INSERT INTO rag_ann_builder (id) VALUES (1);
