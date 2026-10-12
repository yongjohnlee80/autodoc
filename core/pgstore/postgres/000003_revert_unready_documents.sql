-- Undoes 000003_update_unready_documents.sql exactly: the two partial indexes go.
DROP INDEX IF EXISTS rag_document_unready_path;
DROP INDEX IF EXISTS rag_document_unready_id;
