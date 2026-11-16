-- Undoes 000008_update_typed_facets.sql exactly: the value table and the
-- policy's facets go. Keyword pairs stay in rag_document.facets, where the
-- release before it never reads them, until each document's next write.
DROP TABLE IF EXISTS rag_document_value;
ALTER TABLE rag_tenant_policy DROP COLUMN IF EXISTS facets;
