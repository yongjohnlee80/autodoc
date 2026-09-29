-- 000002 revert — the preferences and the embedding providers go; every client
-- falls back to its defaults, and search to words alone.

DROP TABLE IF EXISTS embedding_log;
DROP TABLE IF EXISTS embedding_usage;
DROP TABLE IF EXISTS embedding_provider;
DROP TABLE IF EXISTS preference;
