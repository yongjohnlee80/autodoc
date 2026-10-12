-- The ingest state of a server that indexes a database source into a tenant,
-- and the chunk kind. Additive only: three new tables, keyed by tenant first
-- like the rest, and one column with a default, so every row 000001 wrote is
-- kept and reads as before.

-- A chunk's kind: 'section', a section of the document, or 'abstract', its
-- abstract, as the local store keeps it (sql/deployments 000014). A search
-- for sections only leaves the abstracts out. Every existing chunk is a
-- section.
ALTER TABLE rag_chunk ADD COLUMN kind text NOT NULL DEFAULT 'section';

-- A tenant's position in its source: the (updated_at, id) of the last row a
-- committed page passed, compared lexicographically, so rows sharing a
-- timestamp across a page boundary are never skipped. It is written in the
-- same transaction as the page's documents. NULL updated_at: no page has
-- committed yet. The last full refresh's start and end are kept beside it.
CREATE TABLE rag_cursor (
    tenant           text        NOT NULL PRIMARY KEY,
    updated_at       timestamptz,
    id               text        NOT NULL DEFAULT '',
    refresh_started  timestamptz,
    refresh_finished timestamptz
);

-- A source row that failed to ingest (a missing field, a render error), by
-- its id in the source: quarantined, not skipped. The cursor may pass it,
-- because every pass retries the quarantined ids first; one that ingests, or
-- that the source no longer returns, is released. first_seen is when it first
-- failed, last_seen when it last did.
CREATE TABLE rag_ingest_error (
    tenant     text        NOT NULL,
    source_id  text        NOT NULL,
    error      text        NOT NULL,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant, source_id)
);
CREATE INDEX rag_ingest_error_seen ON rag_ingest_error (tenant, last_seen, source_id);

-- A tenant's access policy, which the server writes from its configuration:
-- the document facet fields every scoped read must be given values for, and
-- the name of the view the tenant is indexed from.
CREATE TABLE rag_tenant_policy (
    tenant        text   NOT NULL PRIMARY KEY,
    access_fields text[] NOT NULL,
    view_name     text   NOT NULL DEFAULT ''
);
