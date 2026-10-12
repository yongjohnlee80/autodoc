// Package edition is what this build of AutoDoc offers beyond its core: the one place a Community
// build and a Pro build differ (ADR 0214 §8).
//
// Databases is the database features: a workspace's source database, where its .view files run,
// and its destination, Postgres with pgvector instead of the local store. When it is off, the
// daemon refuses those settings with ErrDatabases, sys.capabilities says so, and the TUI hides
// them. Flipping it changes no schema and no protocol.
//
// Concurrency: the package is a constant and an error value that nothing reassigns; any goroutine
// may read them.
package edition
