// Package derived makes a derived document's text through the build's deriver, for the indexer and
// the document API alike. The file is handed to the deriver as an io.ReaderAt, read at offsets
// through the vfs.FS when its driver does not give one. A deriver error matching fs.ErrNotExist is
// a cache's eviction miss and is retried once. A text made under another identity than the frozen
// one for its format is refused.
//
// The errors say whose failure it was. ErrNoDeriver is a format the build does not derive.
// ErrDeriverFailed is the deriver's own failure (an error, a second miss, a panic, or no text),
// which may pass and is tried again later. ErrRefused is a text AutoDoc will not take (another
// identity, or a negative stated size), which the same bytes would deliver again. ErrTooLarge is
// text over the reader's bound. A file gone before it could be opened is returned as the vfs.FS
// reported it.
//
// Three bounds apply to a derived document, each its own: MaxContainer bounds the file on disk, so
// a 50 MB PDF that is mostly images is read; MaxText bounds the text the indexer indexes; and the
// document API's own limit (core/docs) bounds what one RPC message carries. The deriver may bound
// its text too (autorag's MaxText); that bound is the deriver's. This package enforces only the
// limit passed to Read: callers apply MaxContainer, and app hands both to the built-in deriver.
//
// Concurrency: the package has no mutable state, and Text and Read may be called from any
// goroutine. Text reads the file through the vfs.FS it is given and calls the deriver on the
// caller's goroutine, so concurrent calls to Text call the deriver's Derive concurrently: the
// build's deriver must allow that. The registrations.Table it reads is frozen when it is built.
// A Derived's Text holds the file open and is one reader's: read it from one goroutine and close
// it once (Read closes it).
package derived
