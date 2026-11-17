-- Undoes 000010_update_source_deletes.sql exactly: the three columns go.
ALTER TABLE rag_document DROP COLUMN IF EXISTS source_at;
ALTER TABLE rag_cursor DROP COLUMN IF EXISTS deletes_at;
ALTER TABLE rag_cursor DROP COLUMN IF EXISTS deletes_id;
