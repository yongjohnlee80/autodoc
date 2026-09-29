-- 000002 — preferences, and the embedding providers: what a client keeps
-- between runs (the TUI's theme, whether its menu bar hides, the side each of
-- its panels docks on), and the providers semantic search can use, with their
-- usage and their recent calls.
--
-- The store's, not a workspace's: a preference is the person's, whichever
-- workspace is open. A name is the client's own ("tui.theme"), and so is its
-- value; the store keeps them as written.

CREATE TABLE IF NOT EXISTS preference (
  name       TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- An embedding provider: its kind (a local ollama, ollama-cloud, or any
-- openai-compatible endpoint), where it is, the model
-- it embeds with, and its API key, SEALED with the store's key (the keyslot
-- file beside the store) and bound to this row's id: a key is never stored as
-- written, and a copied store without the keyslot opens none. The one in use
-- is the preference embedding.provider, by name.
CREATE TABLE IF NOT EXISTS embedding_provider (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  kind       TEXT NOT NULL CHECK (kind IN ('ollama', 'ollama-cloud', 'openai')),
  base_url   TEXT NOT NULL,
  model      TEXT NOT NULL,
  api_key    BLOB,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- A provider's usage by day (UTC): requests, texts sent, the tokens it
-- counted, failed requests, and the failures that were its usage limit.
CREATE TABLE IF NOT EXISTS embedding_usage (
  provider_id INTEGER NOT NULL REFERENCES embedding_provider(id) ON DELETE CASCADE,
  day         TEXT NOT NULL,
  requests    INTEGER NOT NULL DEFAULT 0,
  texts       INTEGER NOT NULL DEFAULT 0,
  tokens      INTEGER NOT NULL DEFAULT 0,
  failures    INTEGER NOT NULL DEFAULT 0,
  limited     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider_id, day)
) WITHOUT ROWID;

-- A provider's recent calls, newest first to a reader: the store keeps the
-- last 200 of each. outcome is "ok", or what went wrong, as a client may show it.
CREATE TABLE IF NOT EXISTS embedding_log (
  id          INTEGER PRIMARY KEY,
  provider_id INTEGER NOT NULL REFERENCES embedding_provider(id) ON DELETE CASCADE,
  at          INTEGER NOT NULL,
  texts       INTEGER NOT NULL,
  tokens      INTEGER NOT NULL,
  millis      INTEGER NOT NULL,
  outcome     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS embedding_log_provider ON embedding_log(provider_id, id);
