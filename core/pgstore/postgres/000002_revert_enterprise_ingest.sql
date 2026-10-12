-- Undoes 000002_update_enterprise_ingest.sql exactly: the ingest state goes,
-- and the chunks lose their kind.
DROP TABLE IF EXISTS rag_tenant_policy;
DROP INDEX IF EXISTS rag_ingest_error_seen;
DROP TABLE IF EXISTS rag_ingest_error;
DROP TABLE IF EXISTS rag_cursor;
ALTER TABLE rag_chunk DROP COLUMN kind;
