-- 000016 — a chunk by its text (ADR 1791329335).
--
-- A workspace keeps a vector only while some chunk, alive or dead, has its text. The writer deletes
-- a text's vectors when it deletes the text's last chunk (gc, a document's removal), stores none for
-- a text no chunk has, and sweeps the rest at its start: each check is "does any chunk of the
-- workspace have this text", a lookup here. It also finds the documents a batch of vectors completes.
CREATE INDEX IF NOT EXISTS chunk_text ON chunk(workspace_id, text_hash);
