-- 000003 — a provider's context window: the most tokens one text may hold,
-- sent to an Ollama provider with every request (num_ctx). The server loads the
-- model at that size, so it decides the model's memory: a model left to the
-- server's own default can take far more than an embedding needs, and during a
-- model switch the active and the target model must both fit at once.
-- 8192 holds any section AutoDoc cuts (about 350 tokens, a code block or a
-- table whole) with room to spare.

ALTER TABLE embedding_provider ADD COLUMN context INTEGER NOT NULL DEFAULT 8192;
