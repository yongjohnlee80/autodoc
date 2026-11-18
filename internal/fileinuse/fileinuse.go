// Package fileinuse says whether an error is a file another program holds in a way that refuses
// this use: a write, a replace or a removal while it's open. That's the holder's to end, not the
// user's error, and a caller says so rather than failing as it would on its own.
//
// The OS-specific part is in this package's per-OS files. On Windows, a file another handle holds
// open without the sharing this use needs refuses it with a sharing or a lock violation. On Unix, a
// file being open refuses nothing. unsupported.go fails the build on any other OS, naming the seam.
package fileinuse
