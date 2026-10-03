-- 000008 — a workspace's own plain-text extensions (ADR 0212 §3).
--
-- A JSON array of lowercased extensions with their dot (".log", ".rst") that
-- the workspace reads as UTF-8 plain text, by declaration: nothing is sniffed.
-- NULL: none. Which files are indexed is still the include and exclude
-- patterns'; this says how an admitted file is read.
ALTER TABLE workspace ADD COLUMN text_extensions TEXT;
