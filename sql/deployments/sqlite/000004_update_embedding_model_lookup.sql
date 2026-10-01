-- Per-model hash lookup and per-workspace chunk limit. NULL retains the default.
-- Correction to 000003's historic comment: the old chunker kept whole code and
-- table blocks even above its nominal maximum. The section bound is enforced by
-- the new chunker; 8192 is not a guaranteed upper bound for every model.
CREATE INDEX embedding_workspace_model_text ON embedding(workspace_id, model_fp, text_hash);
ALTER TABLE workspace ADD COLUMN section_tokens INTEGER;
