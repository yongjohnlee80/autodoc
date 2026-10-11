// Package module is how an AutoDoc feature offers verbs: a Module declares each verb's contract and
// handler, and the public errors its verbs may answer. A host registers them — rpc for the daemon's
// socket, the TUI's script engine for Lua — and everything a client is told about a verb (its
// protocol, its params, the event it logs, the errors it answers, --contracts and AGENTS.md) is
// derived from the contract, so it is said once.
//
// The package is data and context keys only: it depends on the standard library and golib's errs,
// so any core package may import it and no host is imported back.
//
// Concurrency: Contracts, Verbs and PublicErrors are values, read concurrently once built. A Verb's
// Call is safe for concurrent use when the handler it wraps is.
package module
