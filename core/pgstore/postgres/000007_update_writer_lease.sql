-- A tenant's writer lease, in place of the session advisory lock: one row per
-- scope ('tenant:<name>'), claimed and renewed in transactions, every write of
-- the holder fenced in its own transaction (core/pglease). Every time is the
-- database's clock_timestamp(). Only the key is unique: an UPDATE of a column
-- in any unique index would lock the row FOR UPDATE, which a fence's FOR KEY
-- SHARE refuses, and the holder's renew would wait behind its own writes.
CREATE TABLE IF NOT EXISTS rag_writer_lease (
    scope       text COLLATE "C" PRIMARY KEY,
    holder      text NOT NULL,
    epoch       bigint NOT NULL,
    lease_until timestamptz NOT NULL,
    claimed_at  timestamptz,
    renewed_at  timestamptz,
    holder_info text
);
