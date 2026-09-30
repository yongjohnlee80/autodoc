-- 000003 revert — the context window goes; an Ollama provider is left to the
-- server's own context again.

ALTER TABLE embedding_provider DROP COLUMN context;
