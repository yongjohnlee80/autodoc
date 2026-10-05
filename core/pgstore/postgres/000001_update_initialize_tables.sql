-- pgstore's schema. Every table's key starts with tenant, every reference
-- between them carries it, so one tenant's rows never answer for another's.

-- The vector extension must live in a schema the connection searches
-- (public, usually). CREATE EXTENSION only when the database has none: an
-- extension already installed in a schema off the connection's search_path
-- leaves the vector type unfound.
CREATE EXTENSION IF NOT EXISTS vector;

-- Snowball English stemming with no StopWords: every word is kept, so every
-- query word is required and none is dropped, as SQLite's porter tokenizer
-- does. The built-in english configuration cannot be used: its dictionary
-- removes stop words at indexing and at query time, so a query for "not"
-- would find nothing and "not ready" would admit a body with no "not".
-- COPY = simple keeps every other token type (numbers, hosts, files, URLs)
-- on the simple dictionary. The configuration is created in the first schema
-- of the connection's search_path, as the tables are, and every query resolves
-- it by name through the same search_path.
CREATE TEXT SEARCH DICTIONARY rag_english_stem (TEMPLATE = snowball, Language = english);
CREATE TEXT SEARCH CONFIGURATION rag_english (COPY = simple);
ALTER TEXT SEARCH CONFIGURATION rag_english
    ALTER MAPPING FOR asciiword, asciihword, hword_asciipart, word, hword, hword_part
    WITH rag_english_stem;

-- A document: its path within the tenant, presentation (title, tags and
-- field=value facet pairs), the source's version and the identity it was cut
-- under as the consumer compares them (pgstore stores and returns both
-- verbatim and never parses them), its generation (bumped on every write) and
-- whether every chunk has an embedding under the active model. Paths compare
-- byte by byte (COLLATE "C"): "path order" is the same on every server,
-- whatever its locale, and keep/ sorts before keeper/.
CREATE TABLE rag_document (
    tenant         text    NOT NULL,
    id             bigint  GENERATED ALWAYS AS IDENTITY,
    path           text    COLLATE "C" NOT NULL,
    title          text    NOT NULL DEFAULT '',
    tags           text[]  NOT NULL DEFAULT '{}',
    facets         text[]  NOT NULL DEFAULT '{}',
    version        text    NOT NULL DEFAULT '',
    indexer        text    NOT NULL DEFAULT '',
    generation     bigint  NOT NULL DEFAULT 1,
    semantic_ready boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant, id),
    UNIQUE (tenant, path)
);
CREATE INDEX rag_document_tags ON rag_document USING gin (tags);
CREATE INDEX rag_document_facets ON rag_document USING gin (facets);

-- A chunk: a document's section, in order. embed is the text a vector of the
-- chunk is made of when it differs from the breadcrumb and body ('' embeds
-- breadcrumb + line feed + body): embedding is asynchronous and survives
-- restarts, so the text has to be in the row. title and tags are the
-- document's, on every chunk: lexical ranking weighs them above the body, and
-- a registered chunker's breadcrumb need not contain the file name.
-- tsv is generated with the rag_english configuration, four weight classes:
-- title A, breadcrumb B, tags C, body D — the local store's 10 : 5 : 5 : 1
-- column for column.
CREATE TABLE rag_chunk (
    tenant     text    NOT NULL,
    doc        bigint  NOT NULL,
    ord        integer NOT NULL,
    breadcrumb text    NOT NULL DEFAULT '',
    body       text    NOT NULL DEFAULT '',
    embed      text    NOT NULL DEFAULT '',
    title      text    NOT NULL DEFAULT '',
    tags       text    NOT NULL DEFAULT '',
    byte_start integer NOT NULL DEFAULT 0,
    byte_end   integer NOT NULL DEFAULT 0,
    text_hash  bytea   NOT NULL,
    tsv        tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('rag_english'::regconfig, title), 'A') ||
        setweight(to_tsvector('rag_english'::regconfig, breadcrumb), 'B') ||
        setweight(to_tsvector('rag_english'::regconfig, tags), 'C') ||
        setweight(to_tsvector('rag_english'::regconfig, body), 'D')
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