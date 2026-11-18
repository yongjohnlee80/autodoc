-- 000037: a store on PostgreSQL's identity (docs/decisions/store.md#the-daemon-on-postgresql): one
-- row, a random id written once at the store's first open and read at every open, which its daemon
-- reports as store_id. A SQLite store's identity is its file's (store.Identity), so SQLite's script
-- is a comment.
CREATE TABLE IF NOT EXISTS store_identity (
    one boolean PRIMARY KEY DEFAULT true CHECK (one),
    id  text NOT NULL
);
