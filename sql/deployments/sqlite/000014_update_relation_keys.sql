-- 000014 — frontmatter relations as links, and the abstract as a chunk.
--
-- A relation (supersedes, amends, related, sources, adr in a document's frontmatter) is a link row
-- whose kind is its field. It may name its target more than one way: a path both from the
-- workspace root and from the document's own folder. link_key holds those names, tried in ord
-- order, the first that resolves winning; a link whose names change is found by key, as a body
-- link is found by its one name. Body links have no rows here.
CREATE TABLE IF NOT EXISTS link_key (
  workspace_id INTEGER NOT NULL,
  link_id      INTEGER NOT NULL,
  ord          INTEGER NOT NULL,
  key          TEXT NOT NULL,
  PRIMARY KEY (workspace_id, link_id, ord),
  FOREIGN KEY (workspace_id, link_id) REFERENCES link(workspace_id, id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS link_key_key ON link_key(workspace_id, key);

-- A chunk is a document's section, or its abstract (one per document that has one, numbered after
-- its sections). A search may leave the abstracts out before it ranks anything.
-- ('section' or 'abstract'; the indexer writes nothing else).
ALTER TABLE chunk ADD COLUMN kind TEXT NOT NULL DEFAULT 'section';
