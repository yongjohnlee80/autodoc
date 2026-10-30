-- Undoes 000005_update_ann_index_lifecycle.sql: the two tables go. Every HNSW
-- index of the builder's naming on rag_embedding is dropped first, whether its
-- row still names it or not, so none is left with nothing to own it.
DO $$
DECLARE i record;
BEGIN
    FOR i IN SELECT indexname FROM pg_indexes
              WHERE schemaname = current_schema() AND tablename = 'rag_embedding'
                AND indexname LIKE 'rag\_embedding\_hnsw\_%' LOOP
        EXECUTE format('DROP INDEX IF EXISTS %I', i.indexname);
    END LOOP;
END
$$;
DROP TABLE IF EXISTS rag_ann_builder;
DROP TABLE IF EXISTS rag_ann_index;
