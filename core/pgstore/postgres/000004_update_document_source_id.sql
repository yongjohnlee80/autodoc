-- The id of the source row a document was rendered from (an enterprise .view's
-- row id): a web application links a search hit back to its record by it. ''
-- for a document written without one (the local writer's, or one indexed before
-- this migration until its next write). Additive only.
ALTER TABLE rag_document ADD COLUMN source_id text NOT NULL DEFAULT '';
