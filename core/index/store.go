// Package index is a workspace's derived index in the store (core/store): documents, their chunks
// under per-document generations, full-text search over the chunks, tags, aliases, links, and a
// change log. The files are canonical; everything here can be rebuilt from them. Every row is read
// and written through the workspace's store.Scope: this package holds no SQL.
//
// One goroutine writes (the Indexer's writer); any number read, each through one read transaction
// on the WAL, so a reader sees every document at one generation, never half-written.
package index

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// The versions a document records (document.indexer). A document indexed under others is rebuilt,
// even when its file has not changed, so a chunker change reaches every document. SchemaVersion is
// the layout of the rows the indexer writes; the store's own schema is its scripts
// (sql/deployments).
// ChunkerVersion is golib's chunkers' (search/chunk.Version), so a change there rebuilds every
// document.
const (
	ChunkerVersion = chunk.Version
	SchemaVersion  = 3
)

// IndexerVersion is what document.indexer records: "c<chunker>.s<schema>".
var IndexerVersion = "c" + ChunkerVersion + ".s" + strconv.Itoa(SchemaVersion) + ".t512"

func indexerVersion(tokens int) string {
	if tokens <= 0 {
		tokens = store.SectionTokensDefault
	}
	return fmt.Sprintf("c%s.s%d.t%d", ChunkerVersion, SchemaVersion, tokens)
}

// registeredVersion is what document.indexer records for a file a registered chunker cut: its
// extension and the chunker's version, then the layout and the section size, as the built-ins'
// (ADR 0216 §1.6's registered form: "c" EXT "@" VERSION ".s" SCHEMA ".t" TOKENS).
func registeredVersion(ext, version string, tokens int) string {
	if tokens <= 0 {
		tokens = store.SectionTokensDefault
	}
	return fmt.Sprintf("c%s@%s.s%d.t%d", ext, version, SchemaVersion, tokens)
}

// docVersion is what document.indexer records for one path: Markdown files add the fingerprint of
// the workspace's frontmatter schema, so a schema change rebuilds exactly the files it applies to
// (their unchanged chunks keep their vectors, which are keyed by text).
func docVersion(base string, markdown bool, schemaFP string) string {
	if schemaFP != "" && markdown {
		return base + ".f" + schemaFP
	}
	return base
}

func (s *Store) sectionTokens(ctx context.Context) (int, error) {
	return s.db.SectionTokens(ctx, s.sc.ID())
}

// ErrCursorExpired is a change cursor older than the retained log: the client re-lists (0203 §4.5).
var ErrCursorExpired = errors.New("index: the change cursor is older than the retained log")

// Store is one workspace's index in the store.
type Store struct {
	db *store.Store
	sc *store.Scope
}

// Open is workspace's index in db. The store is the caller's: it outlives the index, and Close
// leaves it open.
func Open(db *store.Store, workspace int64) *Store {
	return &Store{db: db, sc: db.Workspace(workspace)}
}

// Close is a no-op: the store is the caller's.
func (s *Store) Close() error { return nil }

// read runs fn in a read transaction of the store.
func (s *Store) read(ctx context.Context, fn func(tx *store.Tx) error) error {
	return s.db.Read(ctx, fn)
}

// outdated lists the documents indexed under another version than the one they would get now
// (want, for a path at the section size): a chunker or section-size change reaches every document
// of that chunker, a schema change every Markdown file.
func (s *Store) outdated(ctx context.Context, want func(path string, tokens int) string) ([]string, error) {
	var out []string
	tokens, err := s.sectionTokens(ctx)
	if err != nil {
		return nil, err
	}
	err = s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.sc.Documents(tx).OrderBy(dao.Asc(store.ByPath)).Select(store.DocPath, store.DocIndexer)
		for _, d := range docs {
			if d.Indexer != want(d.Path, tokens) {
				out = append(out, d.Path)
			}
		}
		return err
	})
	return out, err
}

// Version is the Version a path was indexed at, and whether it is indexed. It is what the
// follower compares with the file's Version (0203 §4.3).
func (s *Store) Version(path string) (vfs.Version, bool) {
	var v string
	err := s.read(context.Background(), func(tx *store.Tx) error {
		d, err := s.sc.Documents(tx).With(store.DocPath, path).Get(store.DocVersion)
		if err == nil {
			v = d.Version
		}
		return err
	})
	return vfs.Version(v), err == nil
}

// PathsUnder lists the indexed paths under dir ("." for all), in path order.
func (s *Store) PathsUnder(dir string) []string {
	var out []string
	_ = s.read(context.Background(), func(tx *store.Tx) error {
		d := s.sc.Documents(tx)
		if dir != "." {
			d = d.WithPredicate(under(`"document"."path"`, dir))
		}
		docs, err := d.OrderBy(dao.Asc(store.ByPath)).Select(store.DocPath)
		for _, x := range docs {
			out = append(out, x.Path)
		}
		return err
	})
	return out
}

// under is the condition that the path column col is dir or below it: the range ["dir/", "dir0")
// holds exactly the paths below dir.
func under(col, dir string) dao.Predicate {
	return dao.Or(dao.Eq(col, dir), dao.And(dao.Gte(col, dir+"/"), dao.Lt(col, dir+"0")))
}

// DocInfo is one document as index.list reports it.
type DocInfo struct {
	Path       string
	Generation int64
	Version    string
}

// List pages the documents in path order after the path after (0203 §4.5).
func (s *Store) List(ctx context.Context, after string, limit int) ([]DocInfo, bool, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var out []DocInfo
	err := s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.sc.Documents(tx).WithPredicate(dao.Gt(`"document"."path"`, after)).
			OrderBy(dao.Asc(store.ByPath)).Limit(uint64(limit+1)).Select(store.DocPath, store.DocActiveGen, store.DocVersion)
		for _, d := range docs {
			out = append(out, DocInfo{Path: d.Path, Generation: d.ActiveGen, Version: d.Version})
		}
		return err
	})
	if err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// Change is one row of the change log.
type Change struct {
	Seq        int64
	Path       string
	Op         string // "upsert" | "delete"
	Generation int64
}

// Changes pages the change log after the cursor since, oldest first. A since older than the
// retained log (since < oldest retained − 1) is ErrCursorExpired. The cursor returned is the last
// row's seq, or since when there are none.
func (s *Store) Changes(ctx context.Context, since int64, limit int) ([]Change, int64, bool, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var out []Change
	err := s.read(ctx, func(tx *store.Tx) error {
		oldest, err := s.oldestRetained(tx)
		if err != nil {
			return err
		}
		if since < oldest-1 {
			return ErrCursorExpired
		}
		rows, err := s.sc.Changes(tx).WithPredicate(dao.Gt(`"change"."seq"`, since)).
			OrderBy(dao.Asc(store.BySeq)).Limit(uint64(limit+1)).Select(store.ChangeSeq, store.ChangePath, store.ChangeOp, store.ChangeGeneration)
		for _, c := range rows {
			out = append(out, Change{Seq: c.Seq, Path: c.Path, Op: c.Op, Generation: c.Generation})
		}
		return err
	})
	if err != nil {
		return nil, since, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	cursor := since
	if len(out) > 0 {
		cursor = out[len(out)-1].Seq
	}
	return out, cursor, more, nil
}

// head is the last change seq ever written (the workspace keeps it past pruning), 0 if none.
func (s *Store) head(tx *store.Tx) (int64, error) {
	w, err := s.sc.Self(tx).Get(store.WorkspaceChangeSeq)
	if err != nil {
		return 0, err
	}
	return w.ChangeSeq, nil
}

// oldestRetained is the oldest change seq still in the log; with none retained, head + 1.
func (s *Store) oldestRetained(tx *store.Tx) (int64, error) {
	c, err := s.sc.Changes(tx).OrderBy(dao.Asc(store.BySeq)).Get(store.ChangeSeq)
	if errors.Is(err, dao.ErrNoRows) {
		h, err := s.head(tx)
		return h + 1, err
	}
	if err != nil {
		return 0, err
	}
	return c.Seq, nil
}

// logChange appends a change row under the workspace's next seq.
func (s *Store) logChange(tx *store.Tx, path, op string, gen, at int64) error {
	if err := s.sc.Self(tx).Set(store.WorkspaceChangeSeq, dao.Incr(1)).Update(); err != nil {
		return err
	}
	seq, err := s.head(tx)
	if err != nil {
		return err
	}
	_, err = s.sc.Changes(tx).Set(store.ChangeSeq, seq).Set(store.ChangePath, path).Set(store.ChangeOp, op).
		Set(store.ChangeGeneration, gen).Set(store.ChangeAt, at).Insert()
	return err
}

// Status is what index.status reports of the store.
type Status struct {
	Cursor         int64 // the change log's head: where index.changes resumes after a full listing
	OldestRetained int64
	Docs, Chunks   int64
	PendingJobs    int64
	// UnparsedFrontmatter lists the documents whose frontmatter did not parse or evaluate; their
	// bodies are indexed all the same.
	UnparsedFrontmatter []string
	// Diagnosed counts the documents whose frontmatter has a diagnostic: malformed YAML, or a field
	// its workspace's schema does not admit. Their bodies are indexed all the same.
	Diagnosed int64
	// Failing lists the jobs whose last attempt failed: a read being retried, or a file over
	// MaxFileSize waiting for its file to change. They are counted in PendingJobs too.
	Failing []JobError
	// Embeddings is the embedding tier's status: nil without a provider (Store.Status never has one;
	// Indexer.Status does when it has a provider).
	Embeddings *EmbeddingStatus
	// Held counts the documents this daemon holds, made by a chunker or a format it lacks (ADR
	// 0216 §1.4): searchable, never reinterpreted. HeldStale are those whose files changed since,
	// HeldUnchecked those the scan or the watcher has not seen yet; both are counted in Held.
	// Store.Status never has them; Indexer.Status does.
	Held, HeldStale, HeldUnchecked int64
}

// JobError is a job whose last attempt failed.
type JobError struct {
	Path     string
	Attempts int
	Err      string
}

// Status reads the store's counts in one snapshot.
func (s *Store) Status(ctx context.Context) (Status, error) {
	var st Status
	err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		if st.Cursor, err = s.head(tx); err != nil {
			return err
		}
		if st.OldestRetained, err = s.oldestRetained(tx); err != nil {
			return err
		}
		n, err := s.sc.Documents(tx).Count()
		if err != nil {
			return err
		}
		st.Docs = int64(n)
		if n, err = alive(s.sc.Chunks(tx)).Count(); err != nil {
			return err
		}
		st.Chunks = int64(n)
		if n, err = s.sc.Jobs(tx).Count(); err != nil {
			return err
		}
		st.PendingJobs = int64(n)
		docs, err := s.sc.Documents(tx).WithPredicate(dao.IsNotNull(`"document"."frontmatter_error"`)).
			OrderBy(dao.Asc(store.ByPath)).Select(store.DocPath)
		if err != nil {
			return err
		}
		for _, d := range docs {
			st.UnparsedFrontmatter = append(st.UnparsedFrontmatter, d.Path)
		}
		if st.Diagnosed, err = countDiagnosed(s.sc.Diagnostics(tx)); err != nil {
			return err
		}
		jobs, err := s.sc.Jobs(tx).WithPredicate(dao.IsNotNull(`"index_job"."last_error"`)).
			OrderBy(dao.Asc(store.ByPath)).Select(store.JobPath, store.JobAttempts, store.JobLastError)
		for _, j := range jobs {
			st.Failing = append(st.Failing, JobError{Path: j.Path, Attempts: int(j.Attempts), Err: deref(j.LastError)})
		}
		return err
	})
	return st, err
}

// alive narrows a chunk query to the chunks alive at their document's active generation: gen_from
// <= active_gen, and gen_to NULL or past it. It joins the document.
func alive[R any, ID any](d dao.DAO[R, store.ChunkField, ID]) dao.DAO[R, store.ChunkField, ID] {
	active := dao.T("document", "active_gen")
	return d.Join(store.JoinDocument).
		WithPredicate(dao.Cmp(dao.T("chunk", "gen_from"), dao.OpLte, active)).
		WithPredicate(dao.Or(dao.IsNull(`"chunk"."gen_to"`), dao.Cmp(dao.T("chunk", "gen_to"), dao.OpGt, active)))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
