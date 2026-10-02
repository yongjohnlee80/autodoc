-- Control when this workspace uses the daemon's shared embedding queue.
-- Existing workspaces continue embedding in the background.
ALTER TABLE workspace ADD COLUMN embedding_policy TEXT NOT NULL DEFAULT 'always'
  CHECK (embedding_policy IN ('always', 'when opened', 'never'));
