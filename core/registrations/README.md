# core/registrations

A build's chunkers and derivers, validated once and frozen when it starts (ADR 0216 §1.2). A main
hands them to `app.Main`, which builds one `*Table` with `New`; the daemon, its indexers and its
clients read it. The community build registers nothing and has the nil `*Table`, whose every
method answers as for no registrations.

## Building a Table

| Name | What |
| --- | --- |
| `New(chunkers, derivers...)` | validates chunkers (by lower-case extension with its dot) and derivers, and freezes them; nil derivers are skipped; none at all is the nil `*Table` |
| `Builtin(own)` | golib's code chunkers (Go, TypeScript and JavaScript, Python, Rust) with a main's own added; an extension in both is refused |
| `Documents(maxContainer, maxText)` | the deriver every build has: `.docx`, `.htm` and `.html` through golib's extractors, no cache |
| `ValidVersion(v)` | whether `v` is a chunker's or deriver's version: 1 to 32 letters, digits, `.`, `_`, `+` or `-` |
| `ValidDeriverID(id)` | whether `id` is a deriver's id: 1 to 64 of the same, plus `/` |
| `Error` | a registration `New` refuses: `Name` (the offender) and `Why` |

## Reading a Table

| Method | Answers |
| --- | --- |
| `Chunker(p)` | the chunker of `p`'s extension, with its frozen version |
| `Has(ext)` | whether `ext` has a registered chunker |
| `Extensions()` | the chunkers' extensions, sorted |
| `Versions()` | the chunkers' frozen versions by extension, a copy |
| `Formats()` | the derived formats, sorted |
| `Format(format)` | a format's frozen identity, and whether the build derives it |
| `Deriver()` | the build's derivers as one, each format going to the deriver that named it; nil for none |
| `CheckDerived(format, d)` | refuses a `Derived` made under another identity than the frozen one |
| `Kinds()` | the extensions the registrations give a kind (`kind.Registrations`) |
| `Collisions(text)` | the workspace plain-text extensions a registration reads instead (ADR 0216 §1.3) |
| `Tables()` | the frozen registrations as a client compares them; empty for none |

## Derivers

| Name | What |
| --- | --- |
| `Deriver` | makes text of derived formats: `Formats`, `Describe` (the identity, read once), `Derive` |
| `Derived` | one file's text: `Text`, `Bytes` (its size, known before reading), `Info`, `ID`, `Version` |
| `Format` | a deriver's identity for one format: `ID` and `Version` (ADR 0216 §1.8) |

## Comparing builds

| Name | What |
| --- | --- |
| `Tables` | chunker versions by extension and deriver identities by format (ADR 0216 §1.4) |
| `Tables.Fingerprint()` | hex sha256 over the sorted entries; builds with the same registrations agree |
| `Tables.StrictSubsetOf(o)` | every entry is in `o` with the same version, and `o` has more: when a TUI offers a restart |
| `Tables.Lacks(o)` | the extensions and formats `o` registers that this does not, or registers otherwise |

A `*Table` is not changed after `New`, so its methods are safe for concurrent use; `Versions` and
`Tables` return copies. The chunkers and derivers it returns are as safe as their registrants made
them, and `Documents`' deriver is safe for concurrent use. A `Tables` value is only read by its
methods; a caller that writes its maps must serialize.
