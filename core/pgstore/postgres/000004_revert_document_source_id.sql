-- Undoes 000004_update_document_source_id.sql exactly: the column goes.
ALTER TABLE rag_document DROP COLUMN IF EXISTS source_id;
