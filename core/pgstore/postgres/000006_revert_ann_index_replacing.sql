-- Undoes 000006_update_ann_index_replacing.sql: a replacement's leftover under
-- its second name is dropped, a replacing row is ready again (its index still
-- serves; the next start finds it outdated and rebuilds it as 000005 does), and
-- the state check is 000005's.
DO $$
DECLARE i record;
BEGIN
    FOR i IN SELECT indexname FROM pg_indexes
              WHERE schemaname = current_schema() AND tablename = 'rag_embedding'
                AND indexname LIKE 'rag\_embedding\_hnsw\_%\_next' LOOP
        EXECUTE format('DROP INDEX IF EXISTS %I', i.indexname);
    END LOOP;
END
$$;
UPDATE rag_ann_index SET state = 'ready' WHERE state = 'replacing';
ALTER TABLE rag_ann_index DROP CONSTRAINT IF EXISTS rag_ann_index_state_check;
ALTER TABLE rag_ann_index ADD CONSTRAINT rag_ann_index_state_check
    CHECK (state IN ('wanted', 'building', 'ready', 'failed', 'retiring'));
