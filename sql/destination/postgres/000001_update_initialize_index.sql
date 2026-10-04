-- 000001 — the index of AutoDoc workspaces in a Postgres destination (ADR 0214 §6-7).
--
-- A workspace whose destination is Postgres keeps its index here instead of in
-- the local store: the documents, their sections, frontmatter, links and
-- vectors. The queue of files to index and the change log stay in the local
-- store; they are the daemon's own state, not the index.
--
-- Several machines' workspaces may share one destination, so a workspace is
-- its uid (the local store's workspace.uid), never its local id, and the uid
-- comes first in every key: partitioning by workspace changes no key and no
-- query (ADR 0206 §4.7).
--
-- Lexical search is a weighted tsvector, generated from the section and
-- indexed with GIN: title above breadcrumb and tags, above the body.
-- Vectors are pgvector's: embedding.vec holds a model's vector at that
-- model's dimensions. The approximate-nearest-neighbour index (HNSW or
-- IVFFlat, cosine distance) needs a fixed dimension, so it is made per model,
-- as a partial index over that model's rows, when the model becomes active.
-- Requires the vector extension in a schema the connection searches (public,
-- usually). It is created here, in the first such schema, only when the
-- database has none: an extension already installed in a schema off the
-- connection's search_path leaves the vector type unfound.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS workspace (
  uid        TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  commit_seq BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS document (
  workspace_uid     TEXT NOT NULL REFERENCES workspace(uid) ON DELETE CASCADE,
  id                BIGINT GENERATED ALWAYS AS IDENTITY,
  path              TEXT NOT NULL,
  version           TEXT NOT NULL,
  active_gen        BIGINT NOT NULL,
  semantic_ready    BOOLEAN NOT NULL DEFAULT false,
  title             TEXT,
  frontmatter       JSONB,
  frontmatter_error TEXT,
  indexer           TEXT NOT NULL,
  indexed_at        BIGINT NOT NULL,
  PRIMARY KEY (workspace_uid, id),
  UNIQUE (workspace_uid, path)
);

CREATE TABLE IF NOT EXISTS chunk (
  workspace_uid TEXT NOT NULL,
  id            BIGINT GENERATED ALWAYS AS IDENTITY,
  doc_id        BIGINT NOT NULL,
  hash          BYTEA NOT NULL,
  text_hash     BYTEA NOT NULL,
  gen_from      BIGINT NOT NULL,
  gen_to        BIGINT,
  ord           INTEGER NOT NULL,
  breadcrumb    TEXT NOT NULL,
  body          TEXT NOT NULL,
  title         TEXT NOT NULL,
  tags          TEXT NOT NULL,
  byte_start    BIGINT NOT NULL,
  byte_end      BIGINT NOT NULL,
  fts           tsvector GENERATED ALWAYS AS (
                  setweight(to_tsvector('english', title), 'A') ||
                  setweight(to_tsvector('english', breadcrumb || ' ' || tags), 'B') ||
                  setweight(to_tsvector('english', body), 'C')) STORED,
  PRIMARY KEY (workspace_uid, id),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS chunk_doc ON chunk (workspace_uid, doc_id, gen_to);
CREATE INDEX IF NOT EXISTS chunk_dead ON chunk (workspace_uid, gen_to) WHERE gen_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS chunk_fts ON chunk USING GIN (fts);

CREATE TABLE IF NOT EXISTS doc_tag (
  workspace_uid TEXT NOT NULL,
  doc_id        BIGINT NOT NULL,
  tag           TEXT NOT NULL,
  PRIMARY KEY (workspace_uid, tag, doc_id),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS doc_tag_doc ON doc_tag (workspace_uid, doc_id);

CREATE TABLE IF NOT EXISTS doc_alias (
  workspace_uid TEXT NOT NULL,
  doc_id        BIGINT NOT NULL,
  alias         TEXT NOT NULL,
  PRIMARY KEY (workspace_uid, alias, doc_id),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS doc_alias_doc ON doc_alias (workspace_uid, doc_id);

CREATE TABLE IF NOT EXISTS doc_name (
  workspace_uid TEXT NOT NULL,
  name_key      TEXT NOT NULL,
  doc_id        BIGINT NOT NULL,
  is_path       BOOLEAN NOT NULL,
  PRIMARY KEY (workspace_uid, name_key, doc_id),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS doc_name_doc ON doc_name (workspace_uid, doc_id, is_path);

CREATE TABLE IF NOT EXISTS doc_facet (
  workspace_uid TEXT NOT NULL,
  doc_id        BIGINT NOT NULL,
  field         TEXT NOT NULL,
  value         TEXT NOT NULL,
  PRIMARY KEY (workspace_uid, field, value, doc_id),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS doc_facet_doc ON doc_facet (workspace_uid, doc_id);

CREATE TABLE IF NOT EXISTS doc_diagnostic (
  workspace_uid TEXT NOT NULL,
  doc_id        BIGINT NOT NULL,
  ord           INTEGER NOT NULL,
  field         TEXT NOT NULL,
  line          INTEGER NOT NULL,
  rule          TEXT NOT NULL,
  message       TEXT NOT NULL,
  PRIMARY KEY (workspace_uid, doc_id, ord),
  FOREIGN KEY (workspace_uid, doc_id) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);

-- dst_doc has no foreign key: the indexer re-resolves a document's inbound
-- links when it goes, and a composite key cannot set dst_doc alone to NULL.
CREATE TABLE IF NOT EXISTS link (
  workspace_uid TEXT NOT NULL,
  id            BIGINT GENERATED ALWAYS AS IDENTITY,
  src_doc       BIGINT NOT NULL,
  gen_from      BIGINT NOT NULL,
  gen_to        BIGINT,
  raw           TEXT NOT NULL,
  name          TEXT NOT NULL,
  dst_doc       BIGINT,
  anchor        TEXT,
  kind          TEXT NOT NULL,
  PRIMARY KEY (workspace_uid, id),
  FOREIGN KEY (workspace_uid, src_doc) REFERENCES document(workspace_uid, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS link_name ON link (workspace_uid, name);
CREATE INDEX IF NOT EXISTS link_src ON link (workspace_uid, src_doc);
CREATE INDEX IF NOT EXISTS link_dst ON link (workspace_uid, dst_doc);

CREATE TABLE IF NOT EXISTS model (
  workspace_uid TEXT NOT NULL REFERENCES workspace(uid) ON DELETE CASCADE,
  fp            TEXT NOT NULL,
  provider      TEXT,
  name          TEXT,
  dims          INTEGER,
  active        BOOLEAN NOT NULL,
  PRIMARY KEY (workspace_uid, fp)
);

CREATE TABLE IF NOT EXISTS embedding (
  workspace_uid TEXT NOT NULL,
  text_hash     BYTEA NOT NULL,
  model_fp      TEXT NOT NULL,
  vec           vector NOT NULL,
  PRIMARY KEY (workspace_uid, text_hash, model_fp),
  FOREIGN KEY (workspace_uid, model_fp) REFERENCES model(workspace_uid, fp) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS embedding_model ON embedding (workspace_uid, model_fp);
