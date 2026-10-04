-- 000002 — a chunk's embed text (ADR 0216 §1.7), as the local store's 000012.
--
-- The text a vector is made of, when a registered chunker's differs from
-- breadcrumb, a line feed and body; '' for the built-in chunkers.
ALTER TABLE chunk ADD COLUMN IF NOT EXISTS embed TEXT NOT NULL DEFAULT '';
