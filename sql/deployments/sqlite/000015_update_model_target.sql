-- 000015 — a workspace's target model, recorded (ADR 1791284787).
--
-- A workspace keeps vectors for its active model and its target only: the model its indexer fills,
-- which is the active one outside a switch. The indexer sets target on the model it fills, and
-- clears it on any other, when it records the model; every model neither active nor target is
-- reclaimed, its vectors and then its row. Recorded here, so the start sweep reaches a workspace
-- whose indexer does not run (no provider, its root unavailable).
-- (0 or 1; at most one a workspace, as active).
ALTER TABLE model ADD COLUMN target INTEGER NOT NULL DEFAULT 0;
