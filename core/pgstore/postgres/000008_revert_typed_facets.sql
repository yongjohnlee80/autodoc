-- Undoes 000008_update_typed_facets.sql exactly: the value table, the
-- policy's facets and the refresh's declaration go. Keyword pairs stay in rag_document.facets, where the
-- release before it never reads them, until each document's next write.
DROP TABLE IF EXISTS rag_document_value;
ALTER TABLE rag_tenant_policy DROP COLUMN IF EXISTS facets;
ALTER TABLE rag_cursor DROP COLUMN IF EXISTS refresh_facets;
