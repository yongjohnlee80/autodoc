# core/kind

How AutoDoc reads a file, decided by its extension and never by its content (ADR 0212 §4). The
indexer, the document API and the editor all ask here, so they agree. In order: `.txt` is `Text`,
`.yaml` and `.yml` are `YAML`, a derived format is `Derived`, a registered chunker's extension is
`Registered`, a workspace's own text extension is `Text`, and anything else is `Markdown`.

| Name | What |
| --- | --- |
| `Kind` | how a file is read: `Markdown`, `Text`, `YAML`, `Derived`, `Registered`; `String` names it |
| `Kind.Traits()`, `Traits` | the kind's facts: `Text` (must be valid UTF-8), `Frontmatter` (checked by a schema), `ReadsAs` (the kind of its served text) |
| `Kind.IsText()` | whether the kind's bytes must pass `Textual` |
| `Of(p, text)` | the kind of `p` in a build with no registrations; `text` is the workspace's own text extensions |
| `Registrations` | a build's `Chunked` and `Derived` extensions; `Of(p, text)` decides with them, `Readable(p)` reports a format the build derives |
| `Resolver` | `Registrations` plus a `Text` func asked on each call; `Of(p)` is the kind of `p` |
| `Table[T]`, `NewTable` | one entry per kind; `NewTable` panics when a kind is missing or an entry names no kind; `Of(k)` is k's entry |
| `DerivedExtensions` | the formats read only as derived text in every build: `.doc`, `.docx`, `.htm`, `.html`, `.odt`, `.pdf` |
| `Ext(p)` | `p`'s extension, lowercased |
| `Label(p)` | a derived format's name for a reader: the extension upper-cased without its dot (`PDF`) |
| `RawText(p)` | whether a derived format's own bytes are text a person edits (`.html`, `.htm`) |
| `Textual(b)` | whether `b` is valid UTF-8 with no NUL byte |
| `IsBuiltIn(ext)` | whether `ext` is `.md`, `.txt`, `.yaml` or `.yml` |
| `ValidExtension(e)` | whether `e` is a dot and 1 to 16 of `a-z 0-9 _ + -` |
| `TextExtensions(in)` | a workspace's own text extensions normalized, or an `*ErrExtension` |
| `ErrExtension` | a text extension that cannot be one; `Ext` and `Why` say which and why |
| `ParseMarkdown(src)` | a workspace's Markdown: GFM with Obsidian's wikilinks, #tags and frontmatter |
| `ParseDerived(src)` | a derived document's text: GFM alone |

No I/O. The functions and methods may be called from any goroutine. Do not modify
`DerivedExtensions`, or a `Registrations` or `Resolver` while others use it. A `Table` is
read-only after `NewTable`. A `Resolver` shared between goroutines needs a `Text` func that is
safe for concurrent use.
