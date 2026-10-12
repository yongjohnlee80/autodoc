// Package config reads and validates an enterprise search server's configuration, server.toml:
// the Postgres it indexes into, the embedding provider, the HTTP API and its clients, and the
// sources, each a .view file indexed into a tenant of its own.
//
// Everything is checked before anything runs. Every refusal names the field it is about, and Load
// returns all of them at once as a *Refusal, so an operator fixes a file in one round. No secret is
// ever written in the file: the Postgres DSN and the embedding key are read from environment
// variables the file names, and an HTTP client's token is kept as its SHA-256 or read from the
// environment. A refusal names the variable, never its value.
//
// The views themselves are loaded and checked by enterprise/ingest; Load checks that each source
// names one.
//
//	cfg, err := config.Load("server.toml")
//	var r *config.Refusal
//	if errors.As(err, &r) {
//		// exit 2: r names every field that is wrong
//	}
//
// Concurrency: Load may be called from any goroutine; a Server is a value it returns and never
// changes after. Postgres.DSN reads the environment when called.
package config
