// Package osaudit holds a guard over the module's structure, test files only: each OS concern is
// one package with an OS-neutral API, and what only one OS can do stays inside it
// (docs/decisions/platforms.md#each-os-concern-is-one-package). Its ledger lists what still does
// it elsewhere, and may only shrink.
package osaudit
