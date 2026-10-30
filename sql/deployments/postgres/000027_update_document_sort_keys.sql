-- 000027 on PostgreSQL, the twin of SQLite's (ADR 1793319634): index.documents orders and pages in SQL (ADR 1793312720 D2): each document's sort keys,
-- stored ascending so that one row-value comparison seeks the order a page resumes in. k_upd_s and
-- k_upd_ns are the updated order's instant (the frontmatter's updated when it parses, else
-- indexed_at), as its Unix seconds and nanoseconds, negated; k_indexed is indexed_at, negated. They
-- are NULL until the writer fills them: a document written from this script on carries them, and
-- the writer backfills the rest at start, a bounded number per commit, while a listing that needs
-- them falls back to reading the documents whole. The partial index answers that fallback's gate.
ALTER TABLE document ADD COLUMN k_upd_s bigint;
ALTER TABLE document ADD COLUMN k_upd_ns bigint;
ALTER TABLE document ADD COLUMN k_indexed bigint;
CREATE INDEX IF NOT EXISTS document_k_updated ON document(workspace_id, k_upd_s, k_upd_ns, path);
CREATE INDEX IF NOT EXISTS document_k_indexed ON document(workspace_id, k_indexed, path);
CREATE INDEX IF NOT EXISTS document_k_unset ON document(workspace_id) WHERE k_upd_s IS NULL;
