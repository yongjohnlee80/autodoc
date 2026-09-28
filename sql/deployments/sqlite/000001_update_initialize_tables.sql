-- 000001 — the store: workspaces, and every workspace's index.
--
-- One store holds every workspace. Everything a workspace owns carries its
-- workspace_id, and every key and every unique constraint on such a table
-- starts with it. Deleting a workspace removes all of it (ON DELETE CASCADE),
-- and a later engine can partition these tables by workspace_id, which
-- requires the partition key in every unique constraint.
--
-- A reference between two tables of one workspace is (workspace_id, id), so
-- a row can never point into another workspace.
--
-- The index is derived from the workspace's files and can always be rebuilt;
-- the workspace table and its patterns are the only rows that are not.
--
-- The baseline has no revert: undoing it would drop the store.

-- The ledger: each applied update script by file name, with its digest.
CREATE TABLE IF NOT EXISTS schema_version (
  script     TEXT PRIMARY KEY,
  sha256     TEXT NOT NULL,
  applied_at INTEGER NOT NULL
);

-- A workspace: a root directory AutoDoc indexes. The id is the identity; the
-- name is what a person types (autodoc --ui <name>) and can be renamed.
-- commit_seq is the last committed index generation; change_seq the last
-- change written to the change log, kept past pruning.
CREATE TABLE IF NOT EXISTS workspace (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  root       TEXT NOT NULL UNIQUE,
  commit_seq INTEGER NOT NULL DEFAULT 0,
  change_seq INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- The file patterns a workspace indexes (include) and skips (exclude), in
-- order. A workspace with no include rows indexes the defaults.
CREATE TABLE IF NOT EXISTS workspace_pattern (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK (kind IN ('include', 'exclude')),
  ord          INTEGER NOT NULL,
  pattern      TEXT NOT NULL,
  PRIMARY KEY (workspace_id, kind, ord)
);

-- A document: one file, by its path under the root. active_gen is the
-- generation readers see; semantic_ready says every live chunk has a vector
-- for the active model.
CREATE TABLE IF NOT EXISTS document (
  id                INTEGER PRIMARY KEY,
  workspace_id      INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  path              TEXT NOT NULL,
  version           TEXT NOT NULL,
  active_gen        INTEGER NOT NULL,
  semantic_ready    INTEGER NOT NULL DEFAULT 0,
  title             TEXT,
  frontmatter_json  TEXT,
  frontmatter_error TEXT,
  indexer           TEXT NOT NULL,
  indexed_at        INTEGER NOT NULL,
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, path)
);

-- A chunk: one section of a document, live from gen_from until gen_to (NULL:
-- still live). hash identifies the chunk, text_hash the text a vector embeds.
CREATE TABLE IF NOT EXISTS chunk (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL,
  doc_id       INTEGER NOT NULL,
  hash         BLOB NOT NULL,
  text_hash    BLOB NOT NULL,
  gen_from     INTEGER NOT NULL,
  gen_to       INTEGER,
  ord          INTEGER NOT NULL,
  breadcrumb   TEXT NOT NULL,
  body         TEXT NOT NULL,
  title        TEXT NOT NULL,
  tags         TEXT NOT NULL,
  byte_start   INTEGER NOT NULL,
  byte_end     INTEGER NOT NULL,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS chunk_doc ON chunk(workspace_id, doc_id, gen_to);
CREATE INDEX IF NOT EXISTS chunk_dead ON chunk(workspace_id, gen_to) WHERE gen_to IS NOT NULL;

-- Full-text search over the chunks: SQLite's own FTS5, an external-content
-- table over chunk. Only the SQLite full-text adapter reads or writes it.
CREATE VIRTUAL TABLE IF NOT EXISTS chunk_fts USING fts5(
  title, breadcrumb, tags, body, workspace_id UNINDEXED,
  content='chunk', content_rowid='id', tokenize='porter unicode61'
);

-- A document's tags and aliases, from its frontmatter.
CREATE TABLE IF NOT EXISTS doc_tag (
  workspace_id INTEGER NOT NULL,
  doc_id       INTEGER NOT NULL,
  tag          TEXT NOT NULL,
  PRIMARY KEY (workspace_id, tag, doc_id),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS doc_tag_doc ON doc_tag(workspace_id, doc_id);

CREATE TABLE IF NOT EXISTS doc_alias (
  workspace_id INTEGER NOT NULL,
  doc_id       INTEGER NOT NULL,
  alias        TEXT NOT NULL,
  PRIMARY KEY (workspace_id, alias, doc_id),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS doc_alias_doc ON doc_alias(workspace_id, doc_id);

-- The names a link can resolve through: a document's path forms (is_path) and
-- its title and aliases, normalized.
CREATE TABLE IF NOT EXISTS doc_name (
  workspace_id INTEGER NOT NULL,
  name_key     TEXT NOT NULL,
  doc_id       INTEGER NOT NULL,
  is_path      INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, name_key, doc_id),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS doc_name_doc ON doc_name(workspace_id, doc_id);

-- A link from one document, live from gen_from until gen_to. dst_doc is the
-- document it resolved to, NULL while it resolves to none. dst_doc has no
-- foreign key: the indexer re-resolves a document's inbound links when it
-- goes, and a composite key cannot set dst_doc alone to NULL.
CREATE TABLE IF NOT EXISTS link (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL,
  src_doc      INTEGER NOT NULL,
  gen_from     INTEGER NOT NULL,
  gen_to       INTEGER,
  raw          TEXT NOT NULL,
  name         TEXT NOT NULL,
  dst_doc      INTEGER,
  anchor       TEXT,
  kind         TEXT NOT NULL,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, src_doc) REFERENCES document(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS link_name ON link(workspace_id, name);
CREATE INDEX IF NOT EXISTS link_src ON link(workspace_id, src_doc);
CREATE INDEX IF NOT EXISTS link_dst ON link(workspace_id, dst_doc);

-- An embedding model a workspace has used, by fingerprint; one is active.
CREATE TABLE IF NOT EXISTS model (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  fp           TEXT NOT NULL,
  provider     TEXT,
  name         TEXT,
  dims         INTEGER,
  active       INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, fp)
) WITHOUT ROWID;

-- A vector for a text under a model: the 1-bit code the search ranks by and
-- the float32 vector it re-scores with. Keyed by text, so an unchanged
-- section keeps its vector across edits.
CREATE TABLE IF NOT EXISTS embedding (
  workspace_id INTEGER NOT NULL,
  text_hash    BLOB NOT NULL,
  model_fp     TEXT NOT NULL,
  bits         BLOB NOT NULL,
  f32          BLOB NOT NULL,
  PRIMARY KEY (workspace_id, text_hash, model_fp),
  FOREIGN KEY (workspace_id, model_fp) REFERENCES model(workspace_id, fp) ON DELETE CASCADE
) WITHOUT ROWID;

-- A file waiting to be indexed.
CREATE TABLE IF NOT EXISTS index_job (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,
  seq          INTEGER NOT NULL,
  reason       TEXT,
  enqueued_at  INTEGER,
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT,
  PRIMARY KEY (workspace_id, path)
) WITHOUT ROWID;

-- The change log a client follows by cursor. seq counts per workspace, from
-- workspace.change_seq, so it keeps rising after old entries are pruned.
CREATE TABLE IF NOT EXISTS change (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,
  path         TEXT NOT NULL,
  op           TEXT NOT NULL,
  generation   INTEGER NOT NULL,
  at           INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, seq)
) WITHOUT ROWID;
