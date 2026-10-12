# core/docs

Reads and writes a workspace's files for AutoDoc's own apps, over the workspace
root's golib `vfs.FS`. A read returns the content with the version it was read at; every write is
conditional on the version the writer read, so no writer replaces what it has not seen, and the
index learns of the write as it learns of an external edit. A derived format (a PDF, a DOCX) reads
as the text the build's deriver makes of it, and is read-only.

| Name | What |
| --- | --- |
| `New(fsys, eligible, opts...)` | the `*Docs` of `fsys` that `eligible` admits (the workspace's include and exclude; `nil` admits every path) |
| `WithTextExtensions(text)` | the workspace's own plain-text extensions, asked at each read and write |
| `WithRegistrations(r)` | reads the build's registered extensions as their kind (UTF-8 text, checked on a write) |
| `WithDeriver(reg)` | reads the formats `reg` derives as their derived text, read-only |
| `Doc` | a document's `Content` at a `Version` |
| `(*Docs).Admits(path)` | whether `path` is a document: eligible, and a format the build reads |
| `(*Docs).Derived(path)` | whether `Read` returns text derived from `path`, not its own bytes |
| `(*Docs).Read(ctx, path)` | the document and the version its content was read at (stat'ed before and after, read again when it moved) |
| `(*Docs).ReadRaw(ctx, path)` | `Read` without deriving, for a derived format whose source is text (HTML); any other derived format is refused |
| `(*Docs).Write(ctx, path, content, want)` | writes if the document is still at `want`; `want` `""` creates it and the folders above it. A stale `want` is a `*vfs.ConflictError` |
| `(*Docs).WriteRaw(ctx, path, content, want)` | `Write` that admits a derived format whose source is text, with UTF-8 content |
| `(*Docs).Rename(ctx, from, to)` | moves a document to a path where nothing is yet |
| `(*Docs).Remove(ctx, path, want)` | deletes the document if it is still at `want` |
| `MaxSize` | the largest document read or written: 4 MiB less 64 KiB |
| `TruncatedMark` | begins the last line of a derived text cut to fit one read |
| `ErrCommitted` | the write landed, then a follow-up step failed: no version is promised; read and compare, never resend |
| `ErrNotEligible` | a path the workspace does not index or a derived format the build does not derive; raw access to a derived format with no raw text; content that is not the UTF-8 text its kind requires |
| `ErrTooLarge` | a document over `MaxSize`, or a derived document's file over `derived.MaxContainer` |
| `ErrReadOnly` | a write, rename or removal of a derived document |

A `Docs` is not changed after `New` and holds no lock: its methods may be called from any number of
goroutines when the `vfs.FS`, `eligible`, the `WithTextExtensions` function and the deriver are safe
for concurrent use. Two writers of one path are kept apart by the `vfs.FS`'s conditional writes, not
by `Docs`.
