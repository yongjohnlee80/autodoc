-- A model's index replaced while it serves. replacing is a valid index of the
-- model whose build parameters are not the declared ones (an upgrade's): it
-- keeps serving searches while the builder makes its replacement under a second
-- name (<name>_next), drops it concurrently and renames the replacement, so
-- searches never fall back to exact for the length of the build.
ALTER TABLE rag_ann_index DROP CONSTRAINT IF EXISTS rag_ann_index_state_check;
ALTER TABLE rag_ann_index ADD CONSTRAINT rag_ann_index_state_check
    CHECK (state IN ('wanted', 'building', 'ready', 'failed', 'retiring', 'replacing'));
