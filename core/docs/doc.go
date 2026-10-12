// Package docs reads and writes a workspace's files for AutoDoc's own apps (ADR 0203 §4.6). A Docs
// works over one workspace root, given as a golib vfs.FS; it never opens a file any other way.
//
// A read returns the content with the version it was read at: the file is stat'ed before and after
// the read, and read again when the version moved, so the version names the bytes returned. Every
// write goes through the vfs, conditional on the version the writer read (a create on the path not
// existing yet), so the index learns of it the same way it learns of an external edit, and no
// writer replaces what it has not seen. A path the workspace does not index is refused
// (ErrNotEligible), and so is a document over MaxSize (ErrTooLarge).
//
// A derived format (a PDF, a DOCX: a file the build's deriver turns into text) reads as its derived
// text, cut to fit one read when it is longer (TruncatedMark), and is read-only (ErrReadOnly).
// ReadRaw and WriteRaw reach the file's own bytes of a derived format whose source is text (HTML).
//
// Concurrency: New sets a Docs's fields and no method changes them; a Docs holds no lock and starts
// no goroutine. Its methods may be called from any number of goroutines when the vfs.FS, the
// eligible function, the WithTextExtensions function and the WithDeriver table's deriver are safe
// for concurrent use. Two writers of one path are kept apart by the vfs.FS's conditional
// operations (CreateExclusive, WriteFileIf, RenameNoReplace, RemoveIf), not by Docs.
package docs
