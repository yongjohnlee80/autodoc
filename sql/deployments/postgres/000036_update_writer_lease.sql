-- 000036: the writer lease of a store on PostgreSQL (docs/decisions/store.md#the-store-on-postgresql):
-- one row, scope 'store', claimed and renewed in transactions (core/pglease), every write of the
-- store confirming it as its first statement. It stands where a SQLite store's file lock does, so
-- SQLite's script is a comment. Only the key is unique: an UPDATE of a column in another unique
-- index would lock the row FOR UPDATE, which a write's FOR KEY SHARE refuses.
CREATE TABLE IF NOT EXISTS writer_lease (
    scope       text COLLATE "C" PRIMARY KEY,
    holder      text NOT NULL,
    epoch       bigint NOT NULL,
    lease_until timestamptz NOT NULL,
    claimed_at  timestamptz,
    renewed_at  timestamptz,
    holder_info text
);
