//go:build !server

package config

// postgresStoreBuild is whether this build has the PostgreSQL store: a build without the server
// tag has none, so [store] engine = "postgres" is refused at load, naming the build.
const postgresStoreBuild = false
