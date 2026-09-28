// Package index is a workspace's derived index: one SQLite file with documents, their chunks under
// per-document generations, full-text search over the chunks, tags, aliases, links, and a change
// log. The files are canonical; everything here can be rebuilt from them.
//
// One goroutine writes (the Indexer's writer); any number read, each through one read transaction
// on the WAL, so a reader sees every document at one generation, never half-written.
package index

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
	"github.com/yongjohnlee80/golib/vfs"
)

// The versions an index records. Each document records the ones it was indexed under, and one
// indexed under others is rebuilt, even when its file has not changed, so a chunker change reaches
// every document. The schema is recorded once: a file of another schema is not opened.
const (
	ChunkerVersion = 1
	SchemaVersion  = 2
)

// IndexerVersion is what document.indexer records: "c<chunker>.s<schema>".
var IndexerVersion = "c" + strconv.Itoa(ChunkerVersion) + ".s" + strconv.Itoa(SchemaVersion)

// ErrSchemaVersion is an index file written under another schema. The index is derived from the
// files, so deleting it rebuilds it.
var ErrSchemaVersion = errors.New("index: the index file has another schema version")

// ErrCursorExpired is a change cursor older than the retained log: the client re-lists (0203 §4.5).
var ErrCursorExpired = errors.New("index: the change cursor is older than the retained log")

const schema = `
CREATE TABLE IF NOT EXISTS document (
  id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE,
  version TEXT NOT NULL,
  active_gen INTEGER NOT NULL,
  semantic_ready INTEGER NOT NULL DEFAULT 0,
  title TEXT, frontmatter_json TEXT, frontmatter_error TEXT,
  indexer TEXT NOT NULL,
  indexed_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS chunk (
  id INTEGER PRIMARY KEY, doc_id INTEGER NOT NULL REFERENCES document ON DELETE CASCADE,
  hash BLOB NOT NULL, text_hash BLOB NOT NULL,
  gen_from INTEGER NOT NULL, gen_to INTEGER,
  ord INTEGER NOT NULL, breadcrumb TEXT NOT NULL, body TEXT NOT NULL,
  title TEXT NOT NULL, tags TEXT NOT NULL,
  byte_start INTEGER NOT NULL, byte_end INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS chunk_doc ON chunk(doc_id, gen_to);
CREATE INDEX IF NOT EXISTS chunk_dead ON chunk(gen_to) WHERE gen_to IS NOT NULL;
CREATE VIRTUAL TABLE IF NOT EXISTS chunk_fts USING fts5(title, breadcrumb, tags, body,
  content='chunk', content_rowid='id', tokenize='porter unicode61');
CREATE TABLE IF NOT EXISTS doc_tag (doc_id INTEGER NOT NULL, tag TEXT NOT NULL, PRIMARY KEY (tag, doc_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS doc_alias (doc_id INTEGER NOT NULL, alias TEXT NOT NULL, PRIMARY KEY (alias, doc_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS link (src_doc INTEGER NOT NULL, gen_from INTEGER NOT NULL, gen_to INTEGER,
  raw TEXT NOT NULL, name TEXT NOT NULL, dst_doc INTEGER, anchor TEXT, kind TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS link_name ON link(name);
CREATE INDEX IF NOT EXISTS link_src ON link(src_doc);
CREATE INDEX IF NOT EXISTS link_dst ON link(dst_doc);
CREATE TABLE IF NOT EXISTS doc_name (key TEXT NOT NULL, doc_id INTEGER NOT NULL, is_path INTEGER NOT NULL,
  PRIMARY KEY (key, doc_id)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS doc_name_doc ON doc_name(doc_id);
CREATE TABLE IF NOT EXISTS model (fp TEXT PRIMARY KEY, provider TEXT, name TEXT, dims INTEGER, active INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS embedding (text_hash BLOB, model_fp TEXT, bits BLOB NOT NULL, f32 BLOB NOT NULL,
  PRIMARY KEY (text_hash, model_fp)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS index_job (path TEXT PRIMARY KEY, seq INTEGER NOT NULL,
  reason TEXT, enqueued_at INTEGER, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT);
CREATE TABLE IF NOT EXISTS change (seq INTEGER PRIMARY KEY AUTOINCREMENT, path TEXT NOT NULL, op TEXT NOT NULL,
  generation INTEGER NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

// Store is one workspace's index file.
type Store struct {
	w dao.DataConn // the one writer: a pool of one, transactions BEGIN IMMEDIATE
	r dao.DataConn // readers: query_only
}

// Open opens (creating and migrating) the index file at path. The caller holds the workspace's
// lease on it (core/workspace), so no other process writes it.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	w, err := sqlite.OpenNamed(ctx, "index-w:"+path, dsn+"&_txlock=immediate", sqlite.MaxOpenConns(1))
	if err != nil {
		return nil, fmt.Errorf("index: opening %s: %w", path, err)
	}
	s := &Store{w: w}
	if err := s.migrate(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	readOnly := func(ctx context.Context, c dao.ConnectedConn) error {
		_, err := c.ExecContext(ctx, "PRAGMA query_only=1")
		return err
	}
	r, err := sqlite.OpenHooked(ctx, "index-r:"+path, dsn, readOnly, sqlite.MaxOpenConns(runtime.NumCPU()))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("index: opening %s for reading: %w", path, err)
	}
	s.r = r
	return s, nil
}

// migrations[v] brings a schema v file to v+1, after the CREATE statements have made what is new.
// Its documents were indexed under the old IndexerVersion, so Run rebuilds each of them.
var migrations = map[int]string{
	1: "", // 2 adds doc_name and the link table's indexes, all made by CREATE
}

// migrate creates the schema, or brings an older one up to date, in one transaction. A file of an
// unknown or newer schema is refused before anything in it changes: CREATE … IF NOT EXISTS would
// leave its tables as they are.
func (s *Store) migrate(ctx context.Context) error {
	var sv string
	var hasMeta int
	if err := scanOne(ctx, s.w, &hasMeta, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'"); err != nil {
		return fmt.Errorf("index: reading the schema: %w", err)
	}
	if hasMeta == 1 {
		if err := scanOne(ctx, s.w, &sv, "SELECT v FROM meta WHERE k = 'schema_version'"); err != nil && !errors.Is(err, errNoRow) {
			return fmt.Errorf("index: reading the schema version: %w", err)
		}
	}
	from := SchemaVersion
	if sv != "" {
		v, err := strconv.Atoi(sv)
		if err != nil || v < 1 || v > SchemaVersion {
			return fmt.Errorf("%w: it has %s, this build %d", ErrSchemaVersion, sv, SchemaVersion)
		}
		from = v
	}
	tx, err := s.w.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("index: creating the schema: %w", err)
	}
	for v := from; v < SchemaVersion; v++ {
		if q := migrations[v]; q != "" {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("index: migrating schema %d to %d: %w", v, v+1, err)
			}
		}
	}
	// the chunker's version is recorded per document (document.indexer), where a rebuild that stops
	// part way still finds the documents it has not reached
	if _, err := tx.ExecContext(ctx, "INSERT INTO meta(k, v) VALUES ('schema_version', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v",
		strconv.Itoa(SchemaVersion)); err != nil {
		return fmt.Errorf("index: recording the schema version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO meta(k, v) VALUES ('commit_seq', '0')"); err != nil {
		return err
	}
	return tx.Commit()
}

// outdated lists the documents indexed under another IndexerVersion.
func (s *Store) outdated(ctx context.Context) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT path FROM document WHERE indexer != ? ORDER BY path", IndexerVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Close closes the file.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

func scanOne(ctx context.Context, q dao.Querier, dst any, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return errNoRow
	}
	if err := rows.Scan(dst); err != nil {
		return err
	}
	return rows.Err()
}

var errNoRow = errors.New("index: no row")

// Version is the Version a path was indexed at, and whether it is indexed. It is what the
// follower compares with the file's Version (0203 §4.3).
func (s *Store) Version(path string) (vfs.Version, bool) {
	var v string
	if err := scanOne(context.Background(), s.r, &v, "SELECT version FROM document WHERE path = ?", path); err != nil {
		return "", false
	}
	return vfs.Version(v), true
}

// PathsUnder lists the indexed paths under dir ("." for all), in path order.
func (s *Store) PathsUnder(dir string) []string {
	ctx := context.Background()
	var rows dao.Rows
	var err error
	if dir == "." {
		rows, err = s.r.QueryContext(ctx, "SELECT path FROM document ORDER BY path")
	} else {
		// dir itself, and dir/… : the range ["dir/", "dir0") holds exactly the paths below it
		rows, err = s.r.QueryContext(ctx, "SELECT path FROM document WHERE path = ? OR (path >= ? AND path < ?) ORDER BY path",
			dir, dir+"/", dir+"0")
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out
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
	rows, err := s.r.QueryContext(ctx, "SELECT path, active_gen, version FROM document WHERE path > ? ORDER BY path LIMIT ?", after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []DocInfo
	for rows.Next() {
		var d DocInfo
		if err := rows.Scan(&d.Path, &d.Generation, &d.Version); err != nil {
			return nil, false, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
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
	tx, err := s.r.Begin(ctx)
	if err != nil {
		return nil, since, false, err
	}
	defer tx.Rollback()
	oldest, err := oldestRetained(ctx, tx)
	if err != nil {
		return nil, since, false, err
	}
	if since < oldest-1 {
		return nil, since, false, ErrCursorExpired
	}
	rows, err := tx.QueryContext(ctx, "SELECT seq, path, op, generation FROM change WHERE seq > ? ORDER BY seq LIMIT ?", since, limit+1)
	if err != nil {
		return nil, since, false, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Seq, &c.Path, &c.Op, &c.Generation); err != nil {
			return nil, since, false, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
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

// head is the last change seq ever written (AUTOINCREMENT keeps it past pruning), 0 if none.
func head(ctx context.Context, q dao.Querier) (int64, error) {
	var h int64
	err := scanOne(ctx, q, &h, "SELECT seq FROM sqlite_sequence WHERE name = 'change'")
	if errors.Is(err, errNoRow) {
		return 0, nil
	}
	return h, err
}

// oldestRetained is the oldest change seq still in the log; with none retained, head + 1.
func oldestRetained(ctx context.Context, q dao.Querier) (int64, error) {
	var o int64
	err := scanOne(ctx, q, &o, "SELECT MIN(seq) FROM change WHERE seq IS NOT NULL HAVING COUNT(*) > 0")
	if errors.Is(err, errNoRow) {
		h, err := head(ctx, q)
		return h + 1, err
	}
	return o, err
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
	// Failing lists the jobs whose last attempt failed: a read being retried, or a note over
	// MaxFileSize waiting for its file to change. They are counted in PendingJobs too.
	Failing []JobError
	// Embeddings is the embedding tier's status: nil without a provider (Store.Status never has one;
	// Indexer.Status does when it has a provider).
	Embeddings *EmbeddingStatus
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
	tx, err := s.r.Begin(ctx)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	if st.Cursor, err = head(ctx, tx); err != nil {
		return st, err
	}
	if st.OldestRetained, err = oldestRetained(ctx, tx); err != nil {
		return st, err
	}
	for _, c := range []struct {
		dst *int64
		q   string
	}{
		{&st.Docs, "SELECT COUNT(*) FROM document"},
		{&st.Chunks, "SELECT COUNT(*) FROM chunk c JOIN document d ON d.id = c.doc_id WHERE c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)"},
		{&st.PendingJobs, "SELECT COUNT(*) FROM index_job"},
	} {
		if err := scanOne(ctx, tx, c.dst, c.q); err != nil {
			return st, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT path FROM document WHERE frontmatter_error IS NOT NULL ORDER BY path")
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return st, err
		}
		st.UnparsedFrontmatter = append(st.UnparsedFrontmatter, p)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	jobs, err := tx.QueryContext(ctx, "SELECT path, attempts, last_error FROM index_job WHERE last_error IS NOT NULL ORDER BY path")
	if err != nil {
		return st, err
	}
	defer jobs.Close()
	for jobs.Next() {
		var j JobError
		if err := jobs.Scan(&j.Path, &j.Attempts, &j.Err); err != nil {
			return st, err
		}
		st.Failing = append(st.Failing, j)
	}
	return st, jobs.Err()
}
