-- Undoes 000010_update_source_deletes.sql exactly: the index and the three
-- columns go.
DROP INDEX IF EXISTS rag_document_source;
ALTER TABLE rag_document DROP COLUMN IF EXISTS source_at;
ALTER TABLE rag_cursor DROP COLUMN IF EXISTS deletes_at;
ALTER TABLE rag_cursor DROP COLUMN IF EXISTS deletes_id;
