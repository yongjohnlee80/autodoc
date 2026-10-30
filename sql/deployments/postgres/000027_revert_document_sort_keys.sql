-- 000027 revert on PostgreSQL — the documents' sort keys go: index.documents reads the documents whole again.
DROP INDEX IF EXISTS document_k_unset;
DROP INDEX IF EXISTS document_k_indexed;
DROP INDEX IF EXISTS document_k_updated;
ALTER TABLE document DROP COLUMN k_indexed;
ALTER TABLE document DROP COLUMN k_upd_ns;
ALTER TABLE document DROP COLUMN k_upd_s;
