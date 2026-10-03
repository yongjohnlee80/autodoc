-- Distinguish an explicit blank include list from the legacy zero-row default.
ALTER TABLE workspace ADD COLUMN include_empty INTEGER NOT NULL DEFAULT 0 CHECK (include_empty IN (0, 1));
