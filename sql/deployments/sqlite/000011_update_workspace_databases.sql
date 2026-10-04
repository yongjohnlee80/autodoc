-- 000011 — a workspace's databases (ADR 0214 §5-7).
--
-- uid identifies the workspace where its local id cannot: in a destination
-- database other machines share. 128 random bits in hex, stable across a
-- rename; this script gives every existing workspace one.
--
-- destination is where the workspace's index and vectors live: the local
-- store ('sqlite', every workspace before this) or a Postgres database
-- ('postgres', its connection in workspace_connection). vector_index is
-- Postgres's pgvector index method; NULL for SQLite, or the default HNSW.
-- view_args are the default arguments of the workspace's .view files, by
-- name, as a JSON object; NULL for none.
--
-- workspace_connection holds at most one source (the database the .view
-- files run on) and one destination per workspace. The DSN is SEALED with the
-- store's key (the keyslot beside the store) and bound to its workspace and
-- role, as an embedding provider's API key is (000002): a password is never
-- stored as written, and a copied store without the keyslot opens none.
ALTER TABLE workspace ADD COLUMN uid TEXT;
UPDATE workspace SET uid = lower(hex(randomblob(16)));
CREATE UNIQUE INDEX workspace_uid ON workspace (uid);

ALTER TABLE workspace ADD COLUMN destination TEXT NOT NULL DEFAULT 'sqlite' CHECK (destination IN ('sqlite', 'postgres'));
ALTER TABLE workspace ADD COLUMN vector_index TEXT CHECK (vector_index IN ('hnsw', 'ivfflat'));
ALTER TABLE workspace ADD COLUMN view_args TEXT;

CREATE TABLE workspace_connection (
  workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  role         TEXT NOT NULL CHECK (role IN ('source', 'destination')),
  engine       TEXT NOT NULL CHECK (engine IN ('postgres', 'sqlite')),
  dsn          BLOB NOT NULL,
  schema_name  TEXT,
  updated_at   INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, role)
);
