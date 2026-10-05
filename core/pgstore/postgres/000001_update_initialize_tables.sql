-- pgstore's schema. Every table's key starts with tenant, every reference
-- between them carries it, so one tenant's rows never answer for another's.

CREATE EXTENSION IF NOT EXISTS vector;

-- A document: its path within the tenant, presentation, filters (tags and
-- field=value facet pairs), its generation (bumped on every write) and whether
-- every chunk has an embedding under the active model. Paths compare byte by
-- byte (COLLATE "C"): "path order" is the same on every server, whatever its
-- locale, and keep/ sorts before keeper/.
CREATE TABLE rag_document (
    tenant         text    NOT NULL,
    id             bigint  GENERATED ALWAYS AS IDENTITY,
    path           text    COLLATE "C" NOT NULL,
    title          text    NOT NULL DEFAULT '',
    tags           text[]  NOT NULL DEFAULT '{}',
    facets         text[]  NOT NULL DEFAULT '{}',
    generation     bigint  NOT NULL DEFAULT 1,
    semantic_ready boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant, id),
    UNIQUE (tenant, path)
);
CREATE INDEX rag_document_tags ON rag_document USING gin (tags);
CREATE INDEX rag_document_facets ON rag_document USING gin (facets);

-- A chunk: a document's section, in order. tsv is generated from the
-- breadcrumb (class A) and the body (class D) with the simple configuration,
-- which lowercases and does not stem.
CREATE TABLE rag_chunk (
    tenant     text    NOT NULL,
    doc        bigint  NOT NULL,
    ord        integer NOT NULL,
    breadcrumb text    NOT NULL DEFAULT '',
    body       text    NOT NULL DEFAULT '',
    byte_start integer NOT NULL DEFAULT 0,
    byte_end   integer NOT NULL DEFAULT 0,
    text_hash  bytea   NOT NULL,
    tsv        tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, breadcrumb), 'A') ||
        setweight(to_tsvector('simple'::regconfig, body), 'D')
    ) STORED,
    PRIMARY KEY (tenant, doc, ord),
    FOREIGN KEY (tenant, doc) REFERENCES rag_document (tenant, id) ON DELETE CASCADE
);
CREATE INDEX rag_chunk_tsv ON rag_chunk USING gin (tsv);
CREATE INDEX rag_chunk_hash ON rag_chunk (tenant, text_hash);

-- An embedding: a chunk text's vector under a model, keyed by the tenant, the
-- model and the text's hash, so two chunks with one text share a vector within
-- a tenant and never across tenants. Dimensions are the model's.
CREATE TABLE rag_embedding (
    tenant    text   NOT NULL,
    model     text   NOT NULL,
    text_hash bytea  NOT NULL,
    vec       vector NOT NULL,
    PRIMARY KEY (tenant, model, text_hash)
);

-- A link from a document to a path, a repeat stored once.
CREATE TABLE rag_link (
    tenant   text   NOT NULL,
    src      bigint NOT NULL,
    dst_path text   NOT NULL,
    PRIMARY KEY (tenant, src, dst_path),
    FOREIGN KEY (tenant, src) REFERENCES rag_document (tenant, id) ON DELETE CASCADE
);
CREATE INDEX rag_link_dst ON rag_link (tenant, dst_path);

-- One row per tenant: the model searches answer under, and the model being
-- filled while a switch is in progress ('' when none).
CREATE TABLE rag_meta (
    tenant       text   NOT NULL PRIMARY KEY,
    active_model text   NOT NULL DEFAULT '',
    target_model text   NOT NULL DEFAULT ''
);
