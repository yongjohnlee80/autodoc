# Index and search decisions

How AutoDoc indexes a workspace and answers a search: what a document write does to the index,
how lexical, semantic and re-ranked results are combined, how builds with their own chunkers and
formats share one store, and how a workspace chooses its files, schema and embedding model. Each
section states the decision as the code implements it now, and names the files that carry it.

## Document generations and chunk reuse

The indexer reads and parses files on parallel workers, but one writer commits to the store. A
document's write is one transaction that builds a new generation of it:

- each chunk is identified by a hash of its chunker version, breadcrumb and body;
- a chunk whose hash is already alive in the document is reused in place, with its position
  updated, so it costs no full-text write and no re-embedding;
- new chunks are inserted under the new generation, and chunks that are gone die at it;
- the document's active generation flips in the same transaction.

A reader therefore sees a document's old text or its new text, never a mix. Dead chunks are
deleted later, in idle batches, so that they do not skew the full-text statistics for long. A file
whose version, chunker and schema are unchanged is skipped, unless the reindex was forced.

In the code: `core/index/indexer.go`, `core/index/chunks.go`

## Hybrid search and rank fusion

`search.query` runs a lexical retriever (SQLite FTS5 with BM25) and, when vectors exist, a semantic
one. Each contributes its top 50 candidates. They are fused by reciprocal rank fusion with k = 60,
which needs no tuning and no score normalisation. After fusion, hits get a small boost for in-links
and for a query word that equals a tag, and each document contributes at most 3 hits.

The semantic retriever scans 1-bit codes held in memory, then rescores the best 200 by float32 dot
product. A query reads one snapshot of the store, and uses an in-memory code index only when it
matches that snapshot; otherwise it scans the stored codes, so it never returns a false-empty
answer. The result says what it used (`ModeUsed`, and `Semantic`: off, ready, partial, switching or
error), so a lexical answer is never passed off as a semantic one.

In the code: `core/index/search.go`, `core/index/semantic.go`, `core/index/engine.go`

## Asynchronous embeddings

Embeddings are optional and run apart from the writer. A vector is keyed by the hash of the exact
text embedded and by the model, so unchanged text is never embedded twice, even across a chunker
change that keeps the text. Texts go to the provider in batches of 64.

- A document joins semantic search only once every alive chunk of it has a vector; until then it
  answers lexically, so it never ranks on a subset of its chunks.
- A new model fills as a target. While it fills, the model it replaces is offline and search
  answers by words (`switching`); the switch happens when the target covers every chunk.
- At start, each workspace keeps only its active model's and its target's vectors.

In the code: `core/index/semantic.go`, `internal/daemon/reclaim.go`

## Link resolution

Links are resolved per workspace, by one function that both the writer and readers use. A
Markdown link reaches its path, or the path with `.md`. A wikilink reaches the document whose path
is its name; otherwise the one document with that file name, path suffix or alias. Of several, the
one nearest the root wins, and a remaining tie is reported as ambiguous rather than guessed. When a
document appears, goes, is renamed or changes its aliases, every link that could match the old or
new name is resolved again in the same transaction.

In the code: `core/index/links.go`, `core/index/graph.go`

## Index status and change log retention

`index.status` reports the document and chunk counts, the follower's state, and the embeddings:
provider, active model, the target being filled, pending and refused texts, and the semantic state.

The change log behind `index.changes` drops a row only when it is both older than 7 days and more
than 100,000 rows behind the head. A client recovering from an old cursor therefore always has at
least a week and 100,000 changes of headroom.

In the code: `core/index/indexer.go`, `core/index/semantic.go`

## Re-ranking with one global ranker

Search has an optional second stage: a ranker model re-scores the top fused candidates (40 by
default, 10 to 100). The daemon has at most one ranker in use, for every workspace; a workspace has
none of its own.

- Rankers are stored like embedding providers: kind (`tei` or `rerank-api`), base URL, model and a
  sealed API key, with usage by day and a log of recent calls. A key is never listed back.
- A change is probed first, then persisted, then swapped in. A search reads the ranker once, at its
  start, so a swap never waits for a search.
- The result reports the stage's state (off, ready or error) and the model that ranked it, and each
  ranked hit carries its score. A failed ranker leaves the hits in recall order.

In the code: `internal/daemon/ranking.go`, `core/store/rankers.go`, `rpc/rankers.go`, `tui/rankers.go`

## A build's own ranking

A build can supply its search ranking through `app.Options.Rank`: its own ranker and window, the
texts the ranker reads for each hit, and whether a search must be ranked or refused.

- A supplied ranker is probed at start and stays in use for the daemon's life. It is never
  replaced by a stored ranker, even when its probe fails; the stage then reports an error.
- It is not persisted. The stored selection is kept, and changes to it are refused, so it comes
  back unchanged for a build that does not supply a ranker.
- With `Required`, a worded search that cannot be ranked is refused instead of answered in recall
  order.

In the code: `app/app.go`, `internal/daemon/ranking.go`, `internal/daemon/workspaces.go`

## One entry point for every build

The binary's modes live in the public package `app`, as `app.Main(ctx, args, Options)`. A main
package only stamps its version and passes in what makes its build its own: chunkers, a deriver and
a ranking. `Main` parses its own flags, never calls `os.Exit`, and returns the exit code (2 for
usage, 1 for an error). The community build is `app.Main` with nothing of its own.

In the code: `app/app.go`, `cmd/autodoc/main.go`

## Registered chunkers and derivers

A build registers chunkers by lower-case file extension and, optionally, a deriver that turns a
document format into text. They are validated once at entry and frozen: a malformed or built-in
extension, a derived format given to a chunker, or a version outside the identity grammar is
refused, naming the offender. Every build already has golib's code chunkers (`.go`, `.ts`, `.tsx`,
`.js`, `.jsx`, `.mjs`, `.py`, `.rs`); a build's own are added to them, and naming one of those
extensions again is refused. Every lookup lower-cases the path's extension.

In the code: `core/registrations/registrations.go`, `core/registrations/builtin.go`, `app/app.go`

## File kinds and their precedence

A file's kind is decided by its extension, in this order: a built-in kind (plain text, YAML), a
derived document format, a registered chunker's, the workspace's own text types, plain text for a
source language, and Markdown for anything else. A registration wins over a workspace text type
with the same extension. Clients take kinds from the daemon, since the daemon does the indexing.
A registered file has no outline beyond its title: a code outline is not part of AutoDoc.

In the code: `core/kind/kind.go`, `core/outline/outline.go`, `core/docs/docs.go`

## Held documents

Every build shares one config, store and socket, and only one daemon serves a store at a time. A
daemon never reinterprets a document that a chunker or format it lacks produced. It holds it
instead:

- the held document stays searchable, with its chunks and vectors;
- it is still deleted when its file is deleted or excluded, since the hold is about
  interpretation, not existence;
- `index.reindex` of it is refused, saying which build or include change would resolve it;
- each hit carries `Hold`: empty for a document this daemon interprets, else `current`, `stale`
  (the file changed since) or `unchecked` (not yet seen by the scan or watcher).

Held status is computed from the stored identity alone, so it is right even before the first scan.
A daemon that has the chunker does reinterpret a document that a build without it indexed as
Markdown, since that is an upgrade. The daemon reports its frozen registrations, and their
fingerprint, in `sys.capabilities`.

In the code: `core/index/hold.go`, `core/index/search.go`, `core/registrations/tables.go`, `rpc/server.go`

## Restarting the daemon as another build

A client compares the daemon's registrations with its own binary's. It offers to restart the daemon
as its own build only when the daemon's are a strict subset of its own, and at most once a session.
A build with fewer registrations never offers to downgrade the daemon, and two builds never flip it
back and forth. Tables that cannot be compared get a note on the status line.

A restart writes a handoff file named for the socket. Every client that would start a daemon checks
it first, and only dials while another live process's handoff stands, so the requester's build is
the one that comes up. The wait is capped on the client's own clock, and a dead, expired or foreign
handoff is ignored. The socket bind remains the lock that keeps two daemons from serving.

In the code: `internal/client/handoff.go`, `tui/registrations.go`, `tui/launch.go`

## Per-chunker versions

A chunk's hash uses its own chunker's version, and the indexer reads the version per path. A change
to one registered chunker re-cuts only that chunker's files, and adding or removing a registration
re-cuts only the files whose kind it changes. The built-in chunkers keep their version, so their
hashes are unchanged.

In the code: `core/index/chunks.go`, `core/index/frontmatter.go`

## Indexer identity strings

Each document records the identity it was indexed under, in one of three forms:

```
built-in   = "c" CHUNKV ".s" SCHEMA ".t" TOKENS [".f" SCHEMAFP]
registered = "c" EXT "@" VERSION ".s" SCHEMA ".t" TOKENS
derived    = "d" EXT ":" DERIVER "@" VERSION "+" built-in
```

`@`, `:` and whitespace never occur inside a field, and a version may contain `+` and `.`, so a
derived string splits at its last `+` and a registered one is read from the right. Every string
parses one way. Versions are 1 to 32 characters of letters, digits, `.`, `_`, `+` or `-`; a deriver
id may also contain `/`. The hold, the skip check and reindexing all read this string.

In the code: `core/index/hold.go`, `core/index/store.go`, `core/registrations/registrations.go`

## Stored embed text

A chunker may embed a text other than its breadcrumb and body, such as a function's signature. That
text is stored on the chunk row, so the embedding worker sends exactly what the chunk's text hash
names, even after a restart or a crash before embedding. The built-in chunkers leave it empty and
embed the breadcrumb and body.

In the code: `core/index/semantic.go`, `core/index/registered_test.go`

## Derived document formats

Every build reads `.docx`, `.htm` and `.html` as derived text, through golib's extractors: an HTML
page is read as its content, without tags, scripts, styles or page chrome. A derived document is
indexed from its text and served read-only. A build's deriver can add other formats, such as PDF,
beside these; a format both name is refused. A deriver's formats and identities are read once at
entry, and a derived text made under another identity is refused. An `.html` file that an older
build indexed as Markdown is read again once, as derived text.

In the code: `core/registrations/deriver.go`, `core/registrations/builtin.go`, `core/kind/kind.go`

## Stored HTML text types

A workspace that stored `.html` or `.htm` as one of its own text types keeps starting. The derived
kind wins, the daemon reports the collision once in its log and in `workspace.list`, and a new text
type that collides is still refused. The Edit tab removes such an entry before validating, with a
one-line note, so saving any setting writes the cleaned list.

In the code: `tui/settings.go`, `internal/daemon/textext.go`

## Opt-in file types

A workspace indexes Markdown, text and YAML by default; code and derived documents are opt-in. The
Edit tab lists the daemon's registered code types and derived document formats beside the built-in
types. Turning a code type on adds its include and writes `vendor/**`, `target/**`, `dist/**` and
`build/**` to the excludes where they are missing, so the excludes are visible and editable rather
than applied by condition. Turning a document format on writes its include only.

In the code: `tui/settings.go`

## A workspace's own text types

A workspace can name extra extensions to read as UTF-8 plain text. They are stored with the
workspace and read at each use, so a change applies without a restart. An extension that a build's
registration reads is refused.

In the code: `internal/daemon/textext.go`, `rpc/workspaceport.go`, `rpc/server.go`

## Frontmatter schemas

A workspace can have a frontmatter schema, a YAML file at a stored path. The daemon keeps the last
valid schema active until the file is valid again, and an edit triggers revalidation of the
workspace's Markdown. Validation never rewrites a note: diagnostics are stored and shown, an invalid
field is left out of the facet index, and the body stays searchable. The indexer and `doc.validate`
use the same validator, so the editor and the index agree.

In the code: `internal/daemon/schema.go`, `rpc/doc.go`, `tui/frontmatter.go`, `core/index/indexer.go`

## Outline, breadcrumbs and previews

The outline and the breadcrumb come from the document's own parse: Markdown and derived documents
by headings, YAML by key paths, plain text by title. The editor outlines its unsaved text, so a
heading typed a moment ago is a jump target. Mermaid diagrams and HTML previews render as images in
a terminal with kitty graphics and a headless browser, offline and in the active theme. Otherwise
HTML opens in the browser and a diagram shows its source with the reason. A preview always ends as
an image or its fallback, never blank.

In the code: `tui/outline.go`, `tui/preview.go`, `rpc/doc.go`, `core/outline/outline.go`

## Workspace providers and peer changes

Each client keeps its own active workspace and view state; the daemon holds no global "current
workspace". A workspace may embed with a stored provider of its own instead of the daemon's.
Switching it rebuilds that workspace's vectors alone, and a provider that cannot be set up leaves
the workspace searching by words rather than falling back to another model.

Configuration and lifecycle changes go to an event log with their kind, workspace and the token of
the client that made them, never a secret or a file's content. Clients poll it with `sys.events`
and tell their user about changes other clients made.

In the code: `internal/daemon/override.go`, `rpc/events.go`, `tui/events.go`, `rpc/workspaceport.go`

## Protocol mismatch recovery

When a client cannot connect because the daemon speaks an older protocol, it opens a dialog that
names both versions and offers Restart Now and Quit. A restart that fails stays in the dialog with
its reason, and Restart Now tries again.

In the code: `tui/session.go`, `tui/preview_test.go`

## Document listing in the store

`index.documents` filters and orders in SQLite rather than in Go. Path, tag, facet and diagnostic
filters are SQL predicates on indexed tables; only `missing` stays in Go, because its rule for a
blank value is Go's own. Each sort is the ascending order of a stored key, with the path as the
tie-break, so a page is one index seek. A full listing costs about the size of the workspace,
instead of a full read and sort for every page.

In the code: `core/index/documents.go`, `core/index/tables.go`, `core/index/documents_keyset_test.go`

## Stored sort keys

The `updated` and `indexed` orders read keys stored on the document: the updated instant's seconds
and nanoseconds, negated, and `indexed_at`, negated, so every order is ascending. One function
computes them, for the document's own write, the start-time backfill and the fallback alike.
Documents from before the keys existed are keyed at start, 2,000 per commit; until a workspace is
fully keyed, a listing that needs the keys takes a Go fallback that orders and resumes by the same
key.

In the code: `core/index/keys.go`, `core/index/indexer.go`, `internal/daemon/reclaim.go`

## Cursors, not offsets

`index.documents` pages by an opaque cursor: `next` encodes the sort, the last returned document's
key and a hash of the filters, and `after` takes it back. A cursor from another sort or other
filters is refused, and so is an integer offset, for every session whatever protocol it declared.
No statement uses `OFFSET`. A document whose key and filter membership stay the same while a
listing pages is seen exactly once; no snapshot across pages is promised.

In the code: `rpc/index.go`, `core/index/documents.go`, `rpc/server.go`

## The workspace map

`graph.map` answers a workspace's documents and the resolved links among them; with a root and a
depth of 1 or 2, only that neighbourhood. Each node carries its title, type, whether it is
superseded, and its tags, and the answer lists the schema's document types, which the GlobeMap
uses for its colours and groups.

In the code: `core/index/graph.go`, `rpc/graph.go`

## Unrooted maps in pages

A map without a root comes a page at a time, in path order after an opaque cursor: 500 documents a
page by default, 2,000 at most. Each page carries the links whose source is on it, so every link is
sent once. A cursor asked under other link kinds is refused. A session from before paging keeps the
one-reply answer. There is no snapshot across pages.

In the code: `core/index/graph.go`, `rpc/graph.go`, `rpc/graphpages_test.go`

## Clients read every page

The GlobeMap reads every page of the map before laying it out, and counts the documents read so far
in its title. A link to a document on a later page waits for that page; one whose document never
arrives is dropped. An answer without a cursor is the whole map.

In the code: `tui/relationmap.go`, `tui/globemap_gui.go`

## Embedding setup

A user can go from no provider to semantic search inside AutoDoc, and every step is a verb an agent
can call too: `host.probe`, `embedding.catalog`, `embedding.estimate`, and the pull job
(`embedding.pull`, `embedding.pull_status`, `embedding.pull_cancel`).

- One pull runs at a time, owned by the daemon rather than the request that started it. Its id is
  never reused, and a stale status or cancel cannot touch a newer job.
- An estimate before the model is installed is provisional: every distinct text, with no model
  fingerprint. After it, the estimate names the real fingerprint, counts the texts still pending
  under it, and times a small sample. Bytes are the index's logical count, not a disk promise.

In the code: `rpc/embedsetup.go`, `rpc/embeddings.go`, `internal/daemon/embedsetup.go`

## Host probe

The probe reads what the machine offers without a network or a provider: GPU vendor, VRAM and
compute capability, RAM, free space under the data directory, and whether Ollama answers. It reads
`/sys` and `/proc` first, and runs `nvidia-smi` only when it is on the path, under a short timeout.
Nothing fails: what cannot be read is reported as unknown.

In the code: `core/hostprobe/hostprobe.go`

## Model recommendation

golib holds the facts about embedding models (sizes, memory at a context, where they fit); which one
to recommend is AutoDoc's policy. On a GPU it is the best model that fits; on a CPU, the lightest
one that still serves many languages.

In the code: `rpc/embedsetup.go`

## The setup dialog

Search models › Embedding Models › Set up… walks the steps in order: probe, catalog, estimate, pull,
then Use. The user picks a model and the workspaces. Use is a plan of existing setters, each
workspace's provider and policy computed apart, applied in an order that never lets an unticked
workspace start embedding with the new model. A failed step stops the plan and Retry runs it again
from the start; a step already done changes nothing. When Ollama updates a model in use, the notice
names the model and says it is refilling.

In the code: `tui/embedsetup.go`, `tui/progress.go`, `tui/qml/dialogs/EmbeddingSetup.qml`
