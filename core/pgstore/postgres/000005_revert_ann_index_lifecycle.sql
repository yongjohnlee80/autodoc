-- Undoes 000005_update_ann_index_lifecycle.sql: the two tables go. The HNSW
-- indexes they recorded are dropped first, so none is left on rag_embedding
-- with nothing to own it.
DO $$
DECLARE i record;
BEGIN
    FOR i IN SELECT name FROM rag_ann_index LOOP
        EXECUTE format('DROP INDEX IF EXISTS %I', i.name);
    END LOOP;
END
$$;
DROP TABLE IF EXISTS rag_ann_builder;
DROP TABLE IF EXISTS rag_ann_index;
