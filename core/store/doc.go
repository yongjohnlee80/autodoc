// Package store is AutoDoc's one store: every workspace, and every workspace's index, in one
// SQLite file. The schema is sql/deployments, applied by golib dao/deploy when the store opens.
// Every row is read and written through golib dao, one declaration per table: the store's own
// (tables.go), and each package's, declared through the seam (Declare, declare.go: the index's,
// the todo journal's). A workspace's tables are reached only through its Scope, which filters
// every read and stamps every write with its workspace.
//
// Open takes the store's lease first: an flock on a file beside the store, named for its device
// and inode, so one process serves a store and another's Open is ErrBusy. The secrets the store
// keeps (an embedding provider's or a ranker's API key, a workspace's database DSN) are sealed
// with its keyslot, a key file beside it.
//
// One connection writes (a pool of one, BEGIN IMMEDIATE); any number read, each in its own read
// transaction on the WAL, so a reader sees one commit. Write and Read run a function in one such
// transaction.
//
// Concurrency: a Store is safe for concurrent use, except Close, which must be its last call.
// Writes are serialized by the one writer connection: a Write waits for the one holding it. Reads
// run in parallel, up to one connection per CPU, and do not wait for writes. A Tx belongs to the
// function it is passed to and ends when that function returns. That function must not, in a
// Write, call a method that uses the writer connection (one that writes, or AutoVacuum, FreePages,
// IncrementalVacuum, Checkpoint, Compact): the outer Write holds the connection, so the call waits
// until its ctx ends. Declare is safe from any goroutine, and panics once a store has opened.
// Between processes, the lease lets one open a store at a time.
package store
