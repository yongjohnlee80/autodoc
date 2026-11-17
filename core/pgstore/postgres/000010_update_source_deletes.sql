-- A source's deletes, read by every pass (a row's deleted flag, or the source's
-- deletes view).
--
-- source_at is the updated_at of the source row last written to the document:
-- a delete at deleted_at removes the document only when source_at is no later,
-- so a row deleted and created again before one pass keeps its new document.
-- NULL (a document written before this script) is deleted by any delete; the
-- next refresh sets it.
ALTER TABLE rag_document ADD COLUMN source_at timestamptz;

-- The deletes view's cursor: the (deleted_at, id) of the last tombstone a pass
-- applied, apart from the rows' cursor. NULL is the view's start.
ALTER TABLE rag_cursor ADD COLUMN deletes_at timestamptz;
ALTER TABLE rag_cursor ADD COLUMN deletes_id text;
