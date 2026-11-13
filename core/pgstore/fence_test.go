package pgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
)

// A Fenced Store fences every write: with its fence refusing, each write method fails with the
// fence's error and writes nothing, while its reads answer as the plain Store's; the plain Store
// it was made from is not fenced at all. The mutants, named here: a write method run as a
// statement of its own outside any transaction (Embed, SetModels, Delete and SetPolicy as they
// were), which writes past a refusing fence.
func TestAFencedStoreFencesEveryWrite(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	plain, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Put(ctx, page("a.md")); err != nil {
		t.Fatal(err)
	}
	refuse := errors.New("the fence refuses")
	calls := 0
	fenced := plain.Fenced(func(context.Context, dao.Querier) error { calls++; return refuse })
	hash := sha256.Sum256([]byte("a text"))
	for _, w := range []struct {
		name string
		do   func() error
	}{
		{"Put", func() error { _, err := fenced.Put(ctx, page("b.md")); return err }},
		{"Delete", func() error { return fenced.Delete(ctx, "a.md") }},
		{"a Batch", func() error {
			b := fenced.Begin(ctx)
			if _, err := b.Put(ctx, page("c.md")); err != nil {
				_ = b.Rollback()
				return err
			}
			return b.Commit()
		}},
		{"Embed", func() error { return fenced.Embed(ctx, "m", []Embedding{{TextHash: hash, Vec: embedVec("a")}}) }},
		{"SetModels", func() error { return fenced.SetModels(ctx, "m", "") }},
		{"SetPolicy", func() error { return fenced.SetPolicy(ctx, Policy{AccessFields: []string{"label_id"}, View: "v"}) }},
		{"MarkReady", func() error { _, err := fenced.MarkReady(ctx, "a.md", "m"); return err }},
		{"ReclaimModels", func() error { _, err := fenced.ReclaimModels(ctx); return err }},
		{"Drop", func() error { return fenced.Drop(ctx) }},
	} {
		before := calls
		if err := w.do(); !errors.Is(err, refuse) {
			t.Errorf("%s past a refusing fence: %v, want the fence's error", w.name, err)
		}
		if calls == before {
			t.Errorf("%s did not run the fence", w.name)
		}
	}
	if got := docPaths(t, plain); got != "a.md" {
		t.Errorf("the documents after every write was refused: %q, want a.md", got)
	}
	if got := docPaths(t, fenced); got != "a.md" {
		t.Errorf("the fenced Store's read: %q, want a.md", got)
	}
	if a, tg, err := fenced.Models(ctx); err != nil || a != "" || tg != "" {
		t.Errorf("the models after SetModels was refused: %q %q, %v", a, tg, err)
	}
	if _, err := plain.Policy(ctx); !errors.Is(err, ErrNoPolicy) {
		t.Errorf("the policy after SetPolicy was refused: %v, want ErrNoPolicy", err)
	}
	var n int64
	if err := scanRows(must(conn.QueryContext(ctx, `SELECT count(*) FROM rag_embedding`)), &n); err != nil || n != 0 {
		t.Errorf("%d embeddings after Embed was refused, %v", n, err)
	}
	// the plain Store is not fenced
	calls = 0
	if _, err := plain.Put(ctx, page("d.md")); err != nil || calls != 0 {
		t.Errorf("the plain Store's Put: %v, the fence ran %d times", err, calls)
	}
}

// The lease table has no unique index but its key: an UPDATE of a column in another unique index
// locks the row FOR UPDATE, which a fence's FOR KEY SHARE refuses, and the holder's renew would
// wait behind its own writes. The mutant, named here: a unique index on holder.
func TestTheLeaseTablesOnlyUniqueIndexIsItsKey(t *testing.T) {
	conn := scratch(t)
	rows, err := conn.QueryContext(context.Background(), `SELECT string_agg(indexrelid::regclass::text, ',' ORDER BY indexrelid::regclass::text)
		FROM pg_index WHERE indrelid = 'rag_writer_lease'::regclass AND indisunique`)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := scanRows(rows, &got); err != nil {
		t.Fatal(err)
	}
	if got != "rag_writer_lease_pkey" {
		t.Errorf("the lease table's unique indexes: %s, want its key alone", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
