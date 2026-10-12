-- The documents not ready for semantic search, indexed apart: an embedding
-- worker reads them (and their chunks) on every round, and a tenant whose
-- documents are almost all ready must not pay for the ones that are. A partial
-- index holds only the unready rows, so it stays as small as the work left.
-- By id, for the worker's pending scan, which resumes at a chunk's
-- (doc, ord); by path, for the listing of unready documents. Additive only.
CREATE INDEX rag_document_unready_id ON rag_document (tenant, id) WHERE NOT semantic_ready;
CREATE INDEX rag_document_unready_path ON rag_document (tenant, path) WHERE NOT semantic_ready;
