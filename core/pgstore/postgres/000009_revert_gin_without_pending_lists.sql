-- Undoes 000009_update_gin_without_pending_lists.sql: the two indexes keep a
-- pending list again (PostgreSQL's default).
ALTER INDEX rag_document_facets RESET (fastupdate);
ALTER INDEX rag_document_tags RESET (fastupdate);
