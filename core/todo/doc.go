// Package todo is AutoDoc's todo system (ADR 1791532020): the task stores, their format, the rules
// and every write. A store is a .todo-list directory of Markdown task files in buckets by status,
// with the archive partitioned by month; the format is auto-core's schema v1 without automation,
// declared as a versioned core/schema file (formats/v1.yaml) and rendered byte for byte as
// auto-core renders it.
//
// The package does no I/O of its own. It reads and writes through ports the daemon provides: the
// Filesystems port opens directories as golib vfs filesystems, file content goes through
// core/docs, the Roots port names the workspaces, and the Journal keeps the change log, snapshots,
// consumers' cursors and the store and root records. A store has a stable id, so a store move
// keeps its claim, snapshot and cursors.
//
// Reading never writes: listing, getting and the archive leave every file as it is. Every write
// names the rev (SHA-256) of the bytes its client saw, and is refused when the file changed
// since. Writes wait for the store's claim, and every one is journaled: a pending row before its
// first file step, committed after its last, settled by recovery after a crash, so a change row
// exists if and only if its file change happened. The default listing reads the active buckets
// only, through a cache keyed by each file's vfs version; archives are read on request, a month at
// a time, and never cached or rewritten by refresh.
//
// A Manager is safe for concurrent use. One mutex per store serializes everything done to that
// store, so a move and every multi-file change is atomic to the daemon's other clients; stores do
// not wait on each other.
package todo
