# core/derived

Makes a derived document's text (a DOCX, an HTML file, a Pro build's PDF) through the build's
deriver, for the indexer and the document API alike. It retries a cache's eviction miss once,
turns a deriver's panic into an error, and refuses text made under another identity than the one
frozen for its format.

| Name | What |
| --- | --- |
| `Text(ctx, reg, fsys, path, size)` | the derived text of `path`, as a `registrations.Derived`; its `Text` holds the file open until the caller closes it |
| `Read(d, limit)` | reads `d`'s text whole, at most `limit` bytes, and closes it; refuses a stated or actual size over `limit` |
| `MaxContainer` | 250 MiB: the largest file a deriver is handed; the callers (`core/index`, `core/docs`) apply it |
| `MaxText` | 16 MiB: the largest derived text the indexer reads |
| `ErrNoDeriver` | the build derives no such format (`errs.ErrInvalidArgument`) |
| `ErrTooLarge` | text over the reader's bound (`errs.ErrInvalidArgument`) |
| `ErrDeriverFailed` | the deriver's own failure: an error, a second miss, a panic, or no text; may pass, so tried again later (`errs.ErrPrecondition`) |
| `ErrRefused` | text AutoDoc will not take, which the same bytes would deliver again: another identity, or a negative stated size (`errs.ErrUnsupported`) |

A file gone before it could be opened is returned as the `vfs.FS` reported it, not as one of these.

No mutable package state: `Text` and `Read` may be called from any goroutine. `Text` reads the file
through the given `vfs.FS` and calls the deriver on the caller's goroutine, so concurrent calls
call the deriver concurrently and the build's deriver must allow that. A `Derived`'s `Text` is one
reader's: read it from one goroutine and close it once.
