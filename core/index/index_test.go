package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/follow"
)

var testMatch = func(p string) bool { return strings.HasSuffix(p, ".md") }

type env struct {
	t     *testing.T
	fsys  *memfs.FS
	fault *faultFS // what the indexer reads through
	dir   string
	store *Store
	ix    *Indexer
	stop  func()
}

func newEnv(t *testing.T, opts Options) *env {
	t.Helper()
	e := &env{t: t, fsys: memfs.New(), dir: t.TempDir()}
	e.fault = &faultFS{FS: e.fsys, fail: map[string]error{}, opens: map[string]int{}}
	e.open(opts)
	return e
}

// faultFS counts the indexer's opens of each path and fails the ones it is told to.
type faultFS struct {
	*memfs.FS
	mu    sync.Mutex
	fail  map[string]error
	opens map[string]int
}

func (f *faultFS) Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	f.mu.Lock()
	f.opens[name]++
	err := f.fail[name]
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.FS.Open(ctx, name, offset)
}

func (f *faultFS) failOpen(p string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, p)
	} else {
		f.fail[p] = err
	}
}

func (f *faultFS) openCount(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[p]
}

// failing checks Status().Failing: only path, with an error saying want; "" for none.
func (e *env) failing(path, want string) {
	e.t.Helper()
	st, err := e.store.Status(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	switch {
	case path == "" && len(st.Failing) != 0:
		e.t.Errorf("Status().Failing = %+v, want none", st.Failing)
	case path != "" && (len(st.Failing) != 1 || st.Failing[0].Path != path || st.Failing[0].Attempts < 1 || !strings.Contains(st.Failing[0].Err, want)):
		e.t.Errorf("Status().Failing = %+v, want %s failing with %q", st.Failing, path, want)
	}
}

// job reads path's job row: whether there is one, its attempts and its last error.
func (e *env) job(p string) (ok bool, attempts int, lastErr string) {
	e.t.Helper()
	err := scanRow(context.Background(), e.store.r, []any{&attempts, &lastErr},
		"SELECT attempts, COALESCE(last_error, '') FROM index_job WHERE path = ?", p)
	if errors.Is(err, errNoRow) {
		return false, 0, ""
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return true, attempts, lastErr
}

// open opens the store and runs an indexer over it, stopping any previous one.
func (e *env) open(opts Options) {
	e.t.Helper()
	if e.stop != nil {
		e.stop()
	}
	s, err := Open(context.Background(), filepath.Join(e.dir, "index.db"))
	if err != nil {
		e.t.Fatal(err)
	}
	if opts.BatchDelay == 0 {
		opts.BatchDelay = 20 * time.Millisecond
	}
	if opts.Match == nil {
		opts.Match = testMatch
	}
	ix := NewIndexer(s, e.fault, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ix.Run(ctx) }()
	e.store, e.ix = s, ix
	stopped := false
	e.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			e.t.Error("the indexer did not stop")
		}
		_ = s.Close()
	}
	e.t.Cleanup(e.stop)
}

func (e *env) write(p, content string) {
	e.t.Helper()
	ctx := context.Background()
	if d := filepath.ToSlash(filepath.Dir(p)); d != "." {
		if err := e.fsys.MkdirAll(ctx, d); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := e.fsys.WriteFile(ctx, p, strings.NewReader(content)); err != nil {
		e.t.Fatal(err)
	}
}

// indexedAt waits until path is indexed at its file's current Version (read again each time: the
// file may change while the test waits) with no job left for it.
func (e *env) indexedAt(p string) {
	e.t.Helper()
	e.eventually("indexing "+p, func() bool {
		fi, err := e.fsys.Stat(context.Background(), p)
		if err != nil {
			return false
		}
		v, ok := e.store.Version(p)
		return ok && v == fi.Version && e.pendingJobs() == 0
	})
}

func (e *env) eventually(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s", what)
}

func (e *env) pendingJobs() int64 {
	st, err := e.store.Status(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return st.PendingJobs
}

type chunkRow struct {
	ord                int
	breadcrumb, body   string
	byteStart, byteEnd int
	genFrom            int64
}

// alive reads a document's chunks alive at its active generation, in ord order.
func (e *env) alive(p string) (gen int64, out []chunkRow) {
	e.t.Helper()
	ctx := context.Background()
	if err := scanOne(ctx, e.store.r, &gen, "SELECT active_gen FROM document WHERE path = ?", p); err != nil {
		e.t.Fatalf("%s: %v", p, err)
	}
	rows, err := e.store.r.QueryContext(ctx, `SELECT c.ord, c.breadcrumb, c.body, c.byte_start, c.byte_end, c.gen_from FROM chunk c
		JOIN document d ON d.id = c.doc_id WHERE d.path = ? AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen) ORDER BY c.ord`, p)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c chunkRow
		if err := rows.Scan(&c.ord, &c.breadcrumb, &c.body, &c.byteStart, &c.byteEnd, &c.genFrom); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, c)
	}
	return gen, out
}

// match runs an FTS query over alive chunks and returns the matching documents' paths.
func (e *env) match(q string) []string {
	e.t.Helper()
	rows, err := e.store.r.QueryContext(context.Background(), `SELECT DISTINCT d.path FROM chunk_fts f JOIN chunk c ON c.id = f.rowid
		JOIN document d ON d.id = c.doc_id WHERE chunk_fts MATCH ? AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen) ORDER BY d.path`, q)
	if err != nil {
		e.t.Fatalf("MATCH %q: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func sections(n int, edit int, text string) string {
	var b strings.Builder
	b.WriteString("# Doc\n\nintro\n\n")
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("section %d body words", i)
		if i == edit {
			body = text
		}
		fmt.Fprintf(&b, "## S%d\n\n%s\n\n", i, body)
	}
	return b.String()
}

// TestIndexesThenSkipsUnchanged: a new file is parsed and chunked; touching it again unchanged is
// skipped by its Version, with no new generation.
func TestIndexesThenSkipsUnchanged(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", sections(3, -1, ""))
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	gen, chunks := e.alive("a.md")
	if len(chunks) != 4 || chunks[1].breadcrumb != "Doc > S0" {
		t.Fatalf("chunks = %+v", chunks)
	}
	parses := e.ix.Parses()
	e.ix.Touch("a.md")
	e.eventually("the job", func() bool { return e.pendingJobs() == 0 })
	time.Sleep(50 * time.Millisecond)
	if g, _ := e.alive("a.md"); g != gen || e.ix.Parses() != parses {
		t.Errorf("an unchanged file was re-indexed: generation %d → %d, parses %d → %d", gen, g, parses, e.ix.Parses())
	}
}

// TestEditOneSectionWritesOnlyThatSection: of a 20-section document, an edit to one section inserts
// one chunk and retires one; the other 20 are reused.
func TestEditOneSectionWritesOnlyThatSection(t *testing.T) {
	e := newEnv(t, Options{noGC: true})
	e.write("a.md", sections(20, -1, ""))
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	e.write("a.md", sections(20, 7, "an edited seventh section"))
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	gen, chunks := e.alive("a.md")
	fresh := 0
	for _, c := range chunks {
		if c.genFrom == gen {
			fresh++
		}
	}
	var dead int
	_ = scanOne(context.Background(), e.store.r, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to = ?", gen)
	if fresh != 1 || dead != 1 || len(chunks) != 21 {
		t.Errorf("edit of one section: %d new chunks, %d retired, %d alive; want 1, 1, 21", fresh, dead, len(chunks))
	}
	if got := e.match(`"edited"`); len(got) != 1 {
		t.Errorf("the edit is not searchable: %v", got)
	}
	if got := e.match(`"S7" AND "section 7 body"`); len(got) != 0 {
		t.Errorf("the retired text is still alive: %v", got)
	}
}

// TestIdenticalSectionsSurviveAndMove: two identical sections both survive an edit before them,
// and the reused chunks' byte ranges follow the edit.
func TestIdenticalSectionsSurviveAndMove(t *testing.T) {
	e := newEnv(t, Options{})
	twin := "## Twin\n\nsame words\n\n"
	e.write("a.md", "# Doc\n\nintro\n\n"+twin+twin)
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	src := "# Doc\n\nintro, now longer than it was\n\n" + twin + twin
	e.write("a.md", src)
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	_, chunks := e.alive("a.md")
	var twins []chunkRow
	for _, c := range chunks {
		if c.body == "same words" {
			twins = append(twins, c)
		}
	}
	if len(twins) != 2 {
		t.Fatalf("identical sections: %+v", chunks)
	}
	for _, c := range twins {
		if got := src[c.byteStart:c.byteEnd]; strings.TrimSpace(got) != "same words" {
			t.Errorf("a reused chunk's range [%d,%d) covers %q, not its text", c.byteStart, c.byteEnd, got)
		}
	}
	if twins[0].byteStart == twins[1].byteStart {
		t.Error("the two twins share one range")
	}
}

// TestTitleAndTagChangeReachFTS: with no body change, a new title and tag match, the old ones do not.
func TestTitleAndTagChangeReachFTS(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "---\ntitle: Alpha plan\ntags: [oldtag]\n---\nbody text\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	if len(e.match(`title:"alpha"`)) != 1 || len(e.match(`tags:"oldtag"`)) != 1 {
		t.Fatal("the first title and tag are not searchable")
	}
	e.write("a.md", "---\ntitle: Omega plan\ntags: [newtag]\n---\nbody text\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	for q, want := range map[string]int{`title:"omega"`: 1, `tags:"newtag"`: 1, `title:"alpha"`: 0, `tags:"oldtag"`: 0} {
		if got := e.match(q); len(got) != want {
			t.Errorf("%s matches %v, want %d", q, got, want)
		}
	}
	// the title is in every breadcrumb, so a title change re-hashes every chunk; a tag change alone
	// reuses them, and only the refresh of their FTS rows can bring the new tag in
	e.write("a.md", "---\ntitle: Omega plan\ntags: [thirdtag]\n---\nbody text\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	for q, want := range map[string]int{`tags:"thirdtag"`: 1, `tags:"newtag"`: 0} {
		if got := e.match(q); len(got) != want {
			t.Errorf("after a tag-only change, %s matches %v, want %d", q, got, want)
		}
	}
}

// TestReindexReparses: a forced job bypasses the fast path.
func TestReindexReparses(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "# A\n\ntext\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	before := e.ix.Parses()
	e.ix.Reindex("a.md")
	e.eventually("the forced reparse", func() bool { return e.ix.Parses() == before+1 })
	e.ix.Reindex("")
	e.eventually("the forced reparse of everything", func() bool { return e.ix.Parses() == before+2 })
}

// TestReaderSeesOneGeneration: a read transaction open across a commit keeps the old text; a new one
// sees the new text; neither sees a mix.
func TestReaderSeesOneGeneration(t *testing.T) {
	e := newEnv(t, Options{noGC: true})
	e.write("a.md", sections(5, -1, ""))
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	ctx := context.Background()
	tx, err := e.store.r.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	bodies := func() []string {
		rows, err := tx.QueryContext(ctx, `SELECT c.body FROM chunk c JOIN document d ON d.id = c.doc_id
			WHERE d.path = 'a.md' AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen) ORDER BY c.ord`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var b string
			_ = rows.Scan(&b)
			out = append(out, b)
		}
		return out
	}
	old := bodies()
	e.write("a.md", sections(5, 2, "rewritten while a reader reads"))
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	if got := bodies(); !reflect.DeepEqual(got, old) {
		t.Errorf("an open read transaction saw the new generation:\n%v\n%v", got, old)
	}
	_, now := e.alive("a.md")
	if now[3].body != "rewritten while a reader reads" {
		t.Errorf("a new reader does not see the new text: %+v", now)
	}
}

// TestDeletedAndIneligibleLeave: a removed file, and one replaced by a directory, leave the index
// and the change log says so.
func TestDeletedAndIneligibleLeave(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "apricot\n")
	e.write("b.md", "blueberry\n")
	e.ix.Touch("a.md")
	e.ix.Touch("b.md")
	e.indexedAt("a.md")
	e.indexedAt("b.md")
	ctx := context.Background()
	if err := e.fsys.Remove(ctx, "a.md"); err != nil {
		t.Fatal(err)
	}
	if err := e.fsys.Remove(ctx, "b.md"); err != nil {
		t.Fatal(err)
	}
	if err := e.fsys.MkdirAll(ctx, "b.md"); err != nil {
		t.Fatal(err)
	}
	e.ix.Touch("a.md")
	e.ix.Touch("b.md")
	e.eventually("both deleted", func() bool { return len(e.store.PathsUnder(".")) == 0 })
	changes, _, _, err := e.store.Changes(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	deletes := 0
	for _, c := range changes {
		if c.Op == "delete" {
			deletes++
		}
	}
	if deletes != 2 {
		t.Errorf("changes = %+v, want two deletes", changes)
	}
	var rowsLeft, entries int
	_ = scanOne(ctx, e.store.r, &rowsLeft, "SELECT COUNT(*) FROM chunk")
	// the FTS index itself: an entry left behind never joins a hit, but it skews BM25
	_ = scanOne(ctx, e.store.r, &entries, "SELECT COUNT(*) FROM chunk_fts WHERE chunk_fts MATCH 'apricot OR blueberry'")
	if rowsLeft != 0 || entries != 0 {
		t.Errorf("after both documents went: %d chunk rows, %d FTS entries", rowsLeft, entries)
	}
}

// TestDeadChunkNeverAHitBeforeGC: a chunk edited away is not a hit even while its row and its FTS
// entry remain, and GC then removes both.
func TestDeadChunkNeverAHitBeforeGC(t *testing.T) {
	e := newEnv(t, Options{noGC: true})
	e.write("a.md", "# A\n\nzebra words\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	e.write("a.md", "# A\n\nlion words\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	if got := e.match(`"zebra"`); len(got) != 0 {
		t.Errorf("a dead chunk is a hit: %v", got)
	}
	var dead int
	_ = scanOne(context.Background(), e.store.r, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
	if dead != 1 {
		t.Fatalf("%d dead rows kept, want 1", dead)
	}
	more, err := e.ix.gc(context.Background())
	if err != nil || more {
		t.Fatalf("gc: %v %v", more, err)
	}
	_ = scanOne(context.Background(), e.store.r, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
	var raw int
	_ = scanOne(context.Background(), e.store.r, &raw, "SELECT COUNT(*) FROM chunk_fts WHERE chunk_fts MATCH '\"zebra\"'")
	if dead != 0 || raw != 0 {
		t.Errorf("after GC: %d dead rows, %d FTS entries for the dead text", dead, raw)
	}
}

// TestJobsSurviveARestart: jobs a previous run left are processed when the next one starts.
func TestJobsSurviveARestart(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "a words\n")
	e.write("b.md", "b words\n")
	e.stop()
	s, err := Open(context.Background(), filepath.Join(e.dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{"a.md", "b.md"} {
		if _, err := s.w.ExecContext(context.Background(), "INSERT INTO index_job(path, seq, reason, enqueued_at) VALUES (?, ?, 'touch', 0)", p, i+1); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	e.stop = nil
	e.open(Options{})
	e.indexedAt("a.md")
	e.indexedAt("b.md")
}

// TestStaleResultIsDiscarded: a path touched again while its worker was reading it is indexed with
// the newer content; the stale result is not committed.
func TestStaleResultIsDiscarded(t *testing.T) {
	var once sync.Once
	var e *env
	e = newEnv(t, Options{afterRead: func(p string) {
		once.Do(func() {
			e.write(p, "newer content\n")
			e.ix.Touch(p)
		})
	}})
	e.write("a.md", "older content\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	if _, chunks := e.alive("a.md"); len(chunks) != 1 || chunks[0].body != "newer content" {
		t.Errorf("chunks = %+v, want the newer content", chunks)
	}
	var changes int
	_ = scanOne(context.Background(), e.store.r, &changes, "SELECT COUNT(*) FROM change")
	if changes != 1 {
		t.Errorf("%d changes: the stale result was committed too", changes)
	}
}

type snapshot struct {
	Docs   []string
	Chunks []string
	Tags   []string
}

func (e *env) snap() snapshot {
	e.t.Helper()
	var s snapshot
	ctx := context.Background()
	collect := func(dst *[]string, q string) {
		rows, err := e.store.r.QueryContext(ctx, q)
		if err != nil {
			e.t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b string
			if err := rows.Scan(&a, &b); err != nil {
				e.t.Fatal(err)
			}
			*dst = append(*dst, a+"|"+b)
		}
	}
	collect(&s.Docs, "SELECT path, title || '|' || version || '|' || COALESCE(frontmatter_json, '') FROM document ORDER BY path")
	collect(&s.Chunks, `SELECT d.path, c.ord || '|' || c.breadcrumb || '|' || c.body || '|' || c.byte_start || '|' || c.byte_end || '|' || hex(c.hash)
		FROM chunk c JOIN document d ON d.id = c.doc_id WHERE c.gen_to IS NULL ORDER BY d.path, c.ord`)
	collect(&s.Tags, "SELECT d.path, t.tag FROM doc_tag t JOIN document d ON d.id = t.doc_id ORDER BY d.path, t.tag")
	return s
}

// TestRebuildFromScratchIsEqual: deleting index.db and indexing again gives the same documents,
// chunks and tags.
func TestRebuildFromScratchIsEqual(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("notes/a.md", "---\ntitle: A\ntags: [x, y]\n---\n# A\n\nbody #inline\n\n## Sub\n\nmore\n")
	e.write("b.md", sections(4, -1, ""))
	for _, p := range []string{"notes/a.md", "b.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	before := e.snap()
	e.stop()
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		_ = os.Remove(filepath.Join(e.dir, f))
	}
	e.stop = nil
	e.open(Options{})
	for _, p := range []string{"notes/a.md", "b.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	if after := e.snap(); !reflect.DeepEqual(before, after) {
		t.Errorf("the rebuilt index differs:\n%+v\n%+v", before, after)
	}
}

// TestOutdatedDocumentsRebuild: documents indexed under another IndexerVersion are rebuilt on the
// next start, even when a start before it opened the store and stopped before indexing anything.
func TestOutdatedDocumentsRebuild(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "a\n")
	e.write("b.md", "b\n")
	e.ix.Touch("a.md")
	e.ix.Touch("b.md")
	e.indexedAt("a.md")
	e.indexedAt("b.md")
	e.stop()
	path := filepath.Join(e.dir, "index.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(context.Background(), "UPDATE document SET indexer = 'c0.s1'"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// the interrupted start: the store is opened, and the process stops before the indexer runs
	s, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	e.stop = nil
	e.open(Options{})
	// "no pending jobs" is already true before the rebuild's jobs are written, so wait for the end
	// state itself: every document carrying this version's indexer
	e.eventually("both documents rebuilt", func() bool {
		var stale int
		_ = scanOne(context.Background(), e.store.r, &stale, "SELECT COUNT(*) FROM document WHERE indexer != ?", IndexerVersion)
		return stale == 0
	})
	if e.ix.Parses() != 2 {
		t.Errorf("%d parses for two unchanged documents, want 2", e.ix.Parses())
	}
}

// TestAnotherSchemaIsRefused: a store written under another schema is not opened, and not marked
// as this one's.
func TestAnotherSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(context.Background(), "UPDATE meta SET v = '0' WHERE k = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	for range 2 {
		if s, err := Open(context.Background(), path); !errors.Is(err, ErrSchemaVersion) {
			if s != nil {
				_ = s.Close()
			}
			t.Fatalf("opening a schema 0 store: %v, want ErrSchemaVersion", err)
		}
	}
}

// TestFrontmatter: valid YAML gives the title, tags and aliases; invalid YAML leaves the body
// indexed and the document flagged.
func TestFrontmatter(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("good.md", "---\ntitle: The Plan\ntags: [Planning, '#q3']\naliases: [plan, roadmap]\n---\nbody #Draft\n")
	e.write("bad.md", "---\ntitle: [unclosed\n---\nthe body still counts\n")
	e.write("h1.md", "# From The Heading\n\ntext\n")
	for _, p := range []string{"good.md", "bad.md", "h1.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	ctx := context.Background()
	var title, fm string
	if err := scanRow(ctx, e.store.r, []any{&title, &fm}, "SELECT title, frontmatter_json FROM document WHERE path = 'good.md'"); err != nil {
		t.Fatal(err)
	}
	if title != "The Plan" || !strings.HasPrefix(fm, `{"title":"The Plan"`) {
		t.Errorf("good.md: title %q, frontmatter %s", title, fm)
	}
	snap := e.snap()
	wantTags := []string{"good.md|draft", "good.md|planning", "good.md|q3"}
	if !reflect.DeepEqual(snap.Tags, wantTags) {
		t.Errorf("tags = %v, want %v", snap.Tags, wantTags)
	}
	var aliases int
	_ = scanOne(ctx, e.store.r, &aliases, "SELECT COUNT(*) FROM doc_alias")
	if aliases != 2 {
		t.Errorf("%d aliases, want 2", aliases)
	}
	st, _ := e.store.Status(ctx)
	if !reflect.DeepEqual(st.UnparsedFrontmatter, []string{"bad.md"}) {
		t.Errorf("unparsed = %v", st.UnparsedFrontmatter)
	}
	if got := e.match(`"still counts"`); !reflect.DeepEqual(got, []string{"bad.md"}) {
		t.Errorf("the body under bad frontmatter: %v", got)
	}
	_ = scanOne(ctx, e.store.r, &title, "SELECT title FROM document WHERE path = 'h1.md'")
	if title != "From The Heading" {
		t.Errorf("h1.md title %q", title)
	}
}

// TestChangeCursor: the log is monotonic and pages; a cursor older than the retained log expires,
// and oldest retained − 1 is still accepted.
func TestChangeCursor(t *testing.T) {
	e := newEnv(t, Options{})
	for i := 0; i < 5; i++ {
		p := fmt.Sprintf("n%d.md", i)
		e.write(p, fmt.Sprintf("note %d\n", i))
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	ctx := context.Background()
	var all []Change
	since := int64(0)
	for {
		page, cursor, more, err := e.store.Changes(ctx, since, 2)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if cursor < since {
			t.Fatalf("the cursor went back: %d → %d", since, cursor)
		}
		since = cursor
		if !more {
			break
		}
	}
	if len(all) != 5 {
		t.Fatalf("paged %d changes, want 5", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Seq <= all[i-1].Seq {
			t.Fatalf("not monotonic: %+v", all)
		}
	}
	if _, err := e.store.w.ExecContext(ctx, "DELETE FROM change WHERE seq <= 2"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.store.Changes(ctx, 1, 10); !errors.Is(err, ErrCursorExpired) {
		t.Errorf("since 1 with oldest 3: %v, want ErrCursorExpired", err)
	}
	if page, _, _, err := e.store.Changes(ctx, 2, 10); err != nil || len(page) != 3 {
		t.Errorf("since = oldest − 1: %d changes, %v", len(page), err)
	}
	st, _ := e.store.Status(ctx)
	if st.OldestRetained != 3 || st.Cursor != 5 {
		t.Errorf("status: oldest %d, cursor %d", st.OldestRetained, st.Cursor)
	}
}

// TestChangeRetention: a row goes only when it is both older than a week and more than 100 000
// behind the head, and oldest_retained is then exact.
func TestChangeRetention(t *testing.T) {
	e := newEnv(t, Options{})
	e.stop()
	s, err := Open(context.Background(), filepath.Join(e.dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	tx, err := s.w.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const n = retainRows + 10
	for i := 1; i <= n; i++ {
		at := now.Unix()
		if i <= 10 {
			at = now.Add(-8 * 24 * time.Hour).Unix() // the first ten are old
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO change(path, op, generation, at) VALUES ('p', 'upsert', 1, ?)", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneChanges(ctx, tx, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// old: 1..10; more than 100 000 behind the head (100 010): 1..10 as well → all ten go
	if st.OldestRetained != 11 || st.Cursor != n {
		t.Errorf("oldest retained %d (want 11), cursor %d (want %d)", st.OldestRetained, st.Cursor, n)
	}
	tx, _ = s.w.Begin(ctx)
	if _, err := tx.ExecContext(ctx, "UPDATE change SET at = ? WHERE seq <= 20", now.Add(-8*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	_ = pruneChanges(ctx, tx, now)
	_ = tx.Commit()
	st, _ = s.Status(ctx)
	// 11..20 are now old, but within 100 000 of the head: they stay
	if st.OldestRetained != 11 {
		t.Errorf("rows within 100 000 of the head were pruned: oldest %d", st.OldestRetained)
	}
}

// TestFollowerIntoIndexer: the follower and the indexer together keep the index on the root —
// 0203 §5.1's changes reach the index, visible through the change log.
func TestFollowerIntoIndexer(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "# A\n\nfirst\n")
	f := follow.New(e.fsys, e.ix, e.ix, follow.Options{PollInterval: 20 * time.Millisecond, MinBackoff: 10 * time.Millisecond, Match: testMatch})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()
	e.indexedAt("a.md")
	start := time.Now()
	e.write("b.md", "# B\n\nsecond\n")
	e.indexedAt("b.md")
	t.Logf("a new file was indexed %v after its write", time.Since(start).Round(time.Millisecond))
	if time.Since(start) > time.Second {
		t.Errorf("indexing took %v, over the 1 s freshness bound", time.Since(start))
	}
	if err := e.fsys.Remove(ctx, "a.md"); err != nil {
		t.Fatal(err)
	}
	e.eventually("a.md deleted", func() bool { _, ok := e.store.Version("a.md"); return !ok })
	changes, _, _, _ := e.store.Changes(ctx, 0, 100)
	var ops []string
	for _, c := range changes {
		ops = append(ops, c.Op+" "+c.Path)
	}
	if !reflect.DeepEqual(ops, []string{"upsert a.md", "upsert b.md", "delete a.md"}) {
		t.Errorf("changes = %v", ops)
	}
}

var _ vfs.FS = (*memfs.FS)(nil)

// TestOversizeFileLeavesAndWaits: a note that grows past MaxFileSize leaves the index, and it is not
// read again until it changes.
func TestOversizeFileLeavesAndWaits(t *testing.T) {
	e := newEnv(t, Options{RetryDelay: 5 * time.Millisecond})
	e.write("a.md", "apricot\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	opens := e.fault.openCount("a.md")
	e.write("a.md", strings.Repeat("x", MaxFileSize+1))
	e.ix.Touch("a.md")
	e.eventually("the oversize note to leave", func() bool {
		_, indexed := e.store.Version("a.md")
		ok, _, lastErr := e.job("a.md")
		return !indexed && ok && strings.Contains(lastErr, "over")
	})
	if got := e.match("apricot"); len(got) != 0 {
		t.Errorf("the oversize note's old text still matches: %v", got)
	}
	time.Sleep(40 * e.ix.opts.RetryDelay)
	// its Stat says it is too large: it is never opened, neither on the touch nor after
	if n := e.fault.openCount("a.md") - opens; n != 0 {
		t.Errorf("the oversize note was opened %d times", n)
	}
	if ok, attempts, _ := e.job("a.md"); !ok || attempts != 1 {
		t.Errorf("job after the wait: present %v, attempts %d; want present, 1", ok, attempts)
	}
	e.failing("a.md", "over")
	e.write("a.md", "blueberry\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	e.failing("", "")
	if len(e.match("blueberry")) != 1 {
		t.Error("the note is not indexed once it is small again")
	}
}

// TestUnreadableFileRetriesWithBackoff: a failing read runs again after a doubling wait, and the
// note is indexed once it can be read.
func TestUnreadableFileRetriesWithBackoff(t *testing.T) {
	// a short batch cycle, so the retry wait is what spaces the reads
	e := newEnv(t, Options{BatchDelay: 2 * time.Millisecond, RetryDelay: 20 * time.Millisecond, MaxRetryDelay: 160 * time.Millisecond})
	e.fault.failOpen("a.md", fs.ErrPermission)
	e.write("a.md", "apricot\n")
	e.ix.Touch("a.md")
	start := time.Now()
	e.eventually("a few failed reads", func() bool { return e.fault.openCount("a.md") >= 3 })
	time.Sleep(600*time.Millisecond - time.Since(start))
	// in 600 ms a fixed 20 ms wait reads it about 27 times; 20, 40, 80, 160, 160 … about 6
	n := e.fault.openCount("a.md")
	t.Logf("%d reads in 600 ms", n)
	if n > 12 {
		t.Errorf("%d reads in 600 ms: the wait does not grow", n)
	}
	if ok, attempts, lastErr := e.job("a.md"); !ok || attempts < 3 || !strings.Contains(lastErr, "permission") {
		t.Errorf("job: present %v, attempts %d, error %q", ok, attempts, lastErr)
	}
	e.failing("a.md", "permission")
	e.fault.failOpen("a.md", nil)
	e.indexedAt("a.md")
	e.failing("", "")
	if len(e.match("apricot")) != 1 {
		t.Error("the note is not indexed once it can be read")
	}
}

func TestRetryDelayDoublesToTheCap(t *testing.T) {
	x := NewIndexer(nil, nil, Options{RetryDelay: 10 * time.Millisecond, MaxRetryDelay: 45 * time.Millisecond})
	var got []time.Duration
	for a := 1; a <= 5; a++ {
		got = append(got, x.retryDelay(a))
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 45 * time.Millisecond, 45 * time.Millisecond}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delays %v, want %v", got, want)
	}
}

// TestReferenceDefinitionsAreNotText: a link reference definition, between blocks or inside a list
// item or a block quote, is in no chunk and no FTS row.
func TestReferenceDefinitionsAreNotText(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "alpha words\n\n[secret]: https://hidden.example/needle\n\nbeta words\n\n"+
		"- [inlist]: https://hidden.example/pinecone\n  gamma words\n\n"+
		"> [quoted]: https://hidden.example/walnut\n> delta words\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	for q, want := range map[string]int{"needle": 0, "pinecone": 0, "walnut": 0, "alpha": 1, "beta": 1, "gamma": 1, "delta": 1} {
		if got := e.match(q); len(got) != want {
			t.Errorf("%s matches %v, want %d", q, got, want)
		}
	}
	_, chunks := e.alive("a.md")
	for _, c := range chunks {
		if strings.Contains(c.body, "hidden.example") {
			t.Errorf("chunk %d holds a definition: %q", c.ord, c.body)
		}
	}
}

// noWatch hides the FS's Watch, so a follower of it polls.
type noWatch struct{ vfs.FS }

// TestReindexEverythingFindsNewFiles: Reindex("") re-parses the indexed documents and, through the
// follower, indexes a file the index has not seen.
func TestReindexEverythingFindsNewFiles(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("a.md", "apricot\n")
	// an hour between polls: only the start's reconcile and a Rescan look at the root
	f := follow.New(noWatch{e.fsys}, e.ix, e.ix, follow.Options{PollInterval: time.Hour, Match: testMatch})
	e.ix.SetRescanner(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()
	e.indexedAt("a.md")
	e.write("new.md", "blueberry\n")
	time.Sleep(100 * time.Millisecond)
	if _, ok := e.store.Version("new.md"); ok {
		t.Fatal("the follower found new.md on its own: this test would show nothing")
	}
	parses := e.ix.Parses()
	e.ix.Reindex("")
	e.indexedAt("new.md")
	e.eventually("a.md re-parsed as well", func() bool { return e.ix.Parses() >= parses+2 })
	if len(e.match("blueberry")) != 1 {
		t.Error("new.md is not searchable")
	}
}
