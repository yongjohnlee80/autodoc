// Package registrations is a build's chunkers and derivers, validated once and frozen when it
// starts (ADR 0216 §1.2): what a main hands app.Main, as the daemon, its indexers and its clients
// read it.
//
// New validates the registrations and freezes them into a *Table. It reads each chunker's version
// and each derived format's identity once, so no document records an identity its text was not
// made under, and CheckDerived refuses a Derived made under another. A build with no registrations,
// the community build, has the nil *Table, and every method answers as for no registrations.
//
// Builtin adds golib's code chunkers to a main's own, and Documents is the deriver every build has
// (.docx, .htm, .html). Tables is the frozen registrations as a client compares them (ADR 0216
// §1.4): Fingerprint, StrictSubsetOf and Lacks tell a TUI whether its daemon reads what it would.
//
// Concurrency: a *Table is not changed after New returns, and its chunkers sit in golib's
// search.Chunkers, which locks, so any number of goroutines may call its methods. Versions and
// Tables return copies. The chunkers and derivers it hands back are the registrants' own: calling
// them concurrently is as safe as they are. Documents' deriver is safe for concurrent use. A
// Tables value's methods only read its maps; a caller that writes those maps must serialize. The
// package has no mutable package-level state.
package registrations
