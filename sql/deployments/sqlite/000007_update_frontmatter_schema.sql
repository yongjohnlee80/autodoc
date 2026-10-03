-- 000007 — frontmatter schemas (ADR 0212 §5).
--
-- A workspace may name a YAML schema file for its notes' frontmatter. The path
-- is the stored authority: absolute, or relative to the workspace's root. NULL:
-- no schema, so no facets and no schema diagnostics.
ALTER TABLE workspace ADD COLUMN schema_path TEXT;

-- doc_facet: the typed values of a document's valid, declared frontmatter
-- fields, defaults included: what an exact filter field:value matches. A list
-- field has one row per item. Derived from the document and the schema, so it
-- can always be rebuilt.
CREATE TABLE IF NOT EXISTS doc_facet (
  workspace_id INTEGER NOT NULL,
  doc_id       INTEGER NOT NULL,
  field        TEXT NOT NULL,
  value        TEXT NOT NULL,
  PRIMARY KEY (workspace_id, field, value, doc_id),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS doc_facet_doc ON doc_facet(workspace_id, doc_id);

-- doc_diagnostic: what is wrong with a document's frontmatter, in source order
-- (ord): the field (empty when it is the whole frontmatter's), the note's line,
-- the rule broken and a message for a person.
CREATE TABLE IF NOT EXISTS doc_diagnostic (
  workspace_id INTEGER NOT NULL,
  doc_id       INTEGER NOT NULL,
  ord          INTEGER NOT NULL,
  field        TEXT NOT NULL,
  line         INTEGER NOT NULL,
  rule         TEXT NOT NULL,
  message      TEXT NOT NULL,
  PRIMARY KEY (workspace_id, doc_id, ord),
  FOREIGN KEY (workspace_id, doc_id) REFERENCES document(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
