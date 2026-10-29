package index

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
)

// rowReads records the statements that read rows (not counts) of the chunk and embedding tables.
type rowReads struct {
	dao.NopHook
	mu    sync.Mutex
	reads []string
	all   []dao.QueryInfo
}

func (c *rowReads) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, *q)
	if (q.Op == dao.OpSelect || q.Op == dao.OpIterate) && (q.Table == "chunk" || q.Table == "embedding") {
		c.reads = append(c.reads, q.SQL)
	}
	return nil
}

// TestTheStatusCountsCoverageInSQLite (AP-2): a status with a model counts the alive chunks, their
// distinct texts and those the model has no vector for without reading a chunk's or a vector's row
// into Go, and the counts are right: two texts shared by three chunks, one of them with a vector.
func TestTheStatusCountsCoverageInSQLite(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "# A\n\nalpha\n", "b.md", "# A\n\nalpha\n", "c.md", "# C\n\ngamma\n")
	var hashes [][]byte
	_ = scanRows(t, e, &hashes, `SELECT DISTINCT text_hash FROM chunk`)
	if len(hashes) != 2 {
		t.Fatalf("%d distinct texts seeded, want 2 (a.md and b.md share one)", len(hashes))
	}
	e.seedVector(e.ws, "fp", hashes[0])
	c := &rowReads{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(c)}
	snap, err := s.statusSnap(context.Background(), "fp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.st.Chunks != 3 || snap.texts != 2 || snap.missing != 1 {
		t.Errorf("chunks %d, texts %d, missing %d; want 3, 2 and 1", snap.st.Chunks, snap.texts, snap.missing)
	}
	if len(c.reads) != 0 {
		t.Errorf("the status read rows of the chunks or vectors into Go:\n%v", c.reads)
	}
}

// scanRows reads one BLOB column of every row q returns.
func scanRows(t *testing.T, e *env, out *[][]byte, q string) error {
	t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		*out = append(*out, b)
	}
	return rows.Err()
}

// failing fails every statement whose SQL holds match, with errFailing.
type failing struct {
	dao.NopHook
	match string
}

var errFailing = errors.New("the store failed")

func (f failing) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	if strings.Contains(q.SQL, f.match) {
		return errFailing
	}
	return nil
}

// TestAStatusWhoseCountFailsSaysSo: a status whose chunk count or coverage count fails returns that
// failure, never a status with a count left at zero.
func TestAStatusWhoseCountFailsSaysSo(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "# A\n\nalpha\n")
	for _, c := range []struct{ name, match, fp string }{
		{"the chunk count, without a model", `SELECT COUNT(*) FROM "chunk"`, ""},
		{"the chunk count, with a model", `SELECT COUNT(*) FROM "chunk"`, "fp"},
		{"the coverage count", "COUNT(DISTINCT", "fp"},
	} {
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(failing{match: c.match})}
		if _, err := s.statusSnap(context.Background(), c.fp, nil); !errors.Is(err, errFailing) {
			t.Errorf("%s failed: the status said %v, want the failure", c.name, err)
		}
	}
}
