// Package follow keeps a workspace's index following its root. The index converges on the root's
// content: watch events, poll events and scans are hints naming paths, and the indexer re-reads
// each path it is told about.
//
// The follower never writes the index. It tells a Queue which paths to re-read, and the indexer
// behind it (the store's one writer) decides from the file's current state what to do: delete a
// document that is gone or no longer eligible, skip one whose Version is unchanged, or re-index it.
//
// Run watches the root through its golib vfs.FS: a native watch when the driver has one, else
// vfs.Poll. When neither can be set up the follower is degraded, scans the whole root every poll
// interval, and retries the setup with backoff. A subtree a scan cannot read keeps its indexed
// documents and is rescanned with backoff. Status reports which of these is happening.
//
// Concurrency: Status and Rescan may be called from any goroutine at any time, including while Run
// runs; Status reads under a mutex, and Rescan never blocks. Run must not be called again until a
// previous call has returned: its retry set is unsynchronized and owned by Run's goroutine. Store,
// Queue and the WithMatch function are called from Run's goroutine only. The vfs.FS and the
// WithExcluded function are also handed to the watch, and vfs.Poll calls them from its own
// goroutine while Run scans, so both must be safe for concurrent use.
package follow
