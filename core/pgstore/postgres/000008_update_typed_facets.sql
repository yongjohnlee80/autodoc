-- A source's typed facets. A keyword facet's values are field=value pairs in
-- rag_document.facets, beside the access pairs, which the GIN index on facets
-- answers. A number or a date facet's values are rows here, one per value (a
-- list value is several rows), each a double: a date as Unix seconds. The
-- primary key answers a range on one field with a scan of (tenant, field,
-- value); the doc index serves a document's rewrite and the cascade that takes
-- its values with it.
CREATE TABLE rag_document_value (
    tenant text             NOT NULL,
    doc_id bigint           NOT NULL,
    field  text             NOT NULL,
    value  double precision NOT NULL,
    PRIMARY KEY (tenant, field, value, doc_id),
    FOREIGN KEY (tenant, doc_id) REFERENCES rag_document (tenant, id) ON DELETE CASCADE
);
CREATE INDEX rag_document_value_doc ON rag_document_value (tenant, doc_id);

-- The tenant's declared facets, name → type (keyword, number or date), beside
-- its access fields: a search reads them in the snapshot that reads the access
-- policy. '{}' declares none, as every policy recorded before this script has.
ALTER TABLE rag_tenant_policy ADD COLUMN facets jsonb NOT NULL DEFAULT '{}';

-- The declaration the tenant's last complete refresh wrote its documents with
-- (pgstore's FacetsDigest; '' is none, as every refresh before this script
-- wrote). A refresh is due at once while it differs from the declaration the
-- ingester indexes with: the values are kept with the documents, and a pass
-- rewrites only the rows that changed.
ALTER TABLE rag_cursor ADD COLUMN refresh_facets text NOT NULL DEFAULT '';
