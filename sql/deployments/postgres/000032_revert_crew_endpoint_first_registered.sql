-- 000032 revert on PostgreSQL — a crew endpoint no longer keeps when its first generation registered.
ALTER TABLE crew_endpoint DROP COLUMN first_registered;
