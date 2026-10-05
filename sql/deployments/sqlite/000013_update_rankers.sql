-- 000013 — ranker models (ADR 0215): the re-ranking models search's second
-- stage can use, one of them in use (the preference ranker.in_use, by name),
-- with the window it ranks (ranker.window).
--
-- kind is 'tei' (Text Embeddings Inference, which serves one model: model is
-- '') or 'rerank-api' (a Cohere-style /rerank endpoint, which names its model).
-- api_key is SEALED with the store's key, bound to the row's id, as an
-- embedding provider's is (000002): a rename never reseals it.
CREATE TABLE IF NOT EXISTS ranker (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  kind       TEXT NOT NULL CHECK (kind IN ('tei', 'rerank-api')),
  base_url   TEXT NOT NULL,
  model      TEXT NOT NULL DEFAULT '',
  api_key    BLOB,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- A ranker's usage by day (UTC), as an embedding provider's (embedding_usage).
CREATE TABLE IF NOT EXISTS ranker_usage (
  ranker_id INTEGER NOT NULL REFERENCES ranker(id) ON DELETE CASCADE,
  day       TEXT NOT NULL,
  requests  INTEGER NOT NULL DEFAULT 0,
  texts     INTEGER NOT NULL DEFAULT 0,
  tokens    INTEGER NOT NULL DEFAULT 0,
  failures  INTEGER NOT NULL DEFAULT 0,
  limited   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ranker_id, day)
) WITHOUT ROWID;

-- A ranker's recent calls: the store keeps the last 200 of each.
CREATE TABLE IF NOT EXISTS ranker_log (
  id        INTEGER PRIMARY KEY,
  ranker_id INTEGER NOT NULL REFERENCES ranker(id) ON DELETE CASCADE,
  at        INTEGER NOT NULL,
  texts     INTEGER NOT NULL,
  tokens    INTEGER NOT NULL,
  millis    INTEGER NOT NULL,
  outcome   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ranker_log_ranker ON ranker_log(ranker_id, id);
