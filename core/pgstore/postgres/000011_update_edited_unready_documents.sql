-- The documents edited since their first write and not ready again yet,
-- indexed apart: an embedding worker reads them first on every round, so an
-- edit behind its scan's cursor is embedded at once rather than after the rest
-- of a backlog. A first load writes every document at generation 1, so the
-- index stays empty through it, and the worker's extra read costs a probe.
-- Additive only.
CREATE INDEX rag_document_edited_unready ON rag_document (tenant, id) WHERE NOT semantic_ready AND generation > 1;
