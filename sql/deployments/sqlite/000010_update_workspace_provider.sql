-- 000010 — a workspace's own embedding provider (ADR 0212 §7).
--
-- NULL: the workspace uses the daemon's provider (the preference
-- embedding.provider), as every workspace did before. Set: it embeds with
-- this stored provider instead, and switching it rebuilds this workspace's
-- vectors alone. Deleting the provider returns the workspace to the daemon's.
ALTER TABLE workspace ADD COLUMN provider_id INTEGER REFERENCES embedding_provider(id) ON DELETE SET NULL;
