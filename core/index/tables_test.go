package index

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE INDEX'S TABLES, SCOPED — the index's tables are declared to the store through its seam, and
// reached only through the workspace's scope (store.Scoped). These checks were the store's while it
// declared them: every workspace-owned table in the catalog has an accessor, the store's or the
// index's, and none of them reaches another workspace's rows, through a statement or a join.

// openDB is a new store, closed when t ends.
func openDB(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// rawDB is a connection of its own to s's file, to read its catalog.
func rawDB(t *testing.T, s *store.Store) dao.DataConn {
	t.Helper()
	db, err := sqlite.Open(context.Background(), "file:"+s.Path()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// accessors is every Scope accessor, by the table it reaches.
func accessors(x *Store, tx *store.Tx) map[string]dao.Named {
	return map[string]dao.Named{
		"workspace_pattern": x.sc.Patterns(tx), "document": x.documents(tx), "chunk": x.chunks(tx),
		"doc_tag": x.tags(tx), "doc_alias": x.aliases(tx), "doc_name": x.names(tx),
		"doc_facet": x.facets(tx), "doc_diagnostic": x.diagnostics(tx),
		"link": x.linksOut(tx), "link_key": x.linkKeys(tx), "model": x.sc.Models(tx), "embedding": x.embeddings(tx),
		"index_job": x.jobs(tx), "change": x.changes(tx), "workspace_connection": x.sc.Connections(tx),
		"workspace_graph_only": x.sc.GraphOnly(tx),
	}
}

// Every table the live store has with a workspace_id column has an accessor, the store's
// (its Scope) or the index's own: read from the store's catalog, not from the declarations, so a new
// table a script adds is caught when it has none. chunk_fts is the full-text
// index, reached only through Chunks' join.
func TestEveryWorkspaceOwnedTableHasAScopeAccessor(t *testing.T) {
	ctx := context.Background()
	s := openDB(t)
	raw := rawDB(t, s)
	tables, err := dao.ListTables(ctx, raw, "")
	if err != nil {
		t.Fatal(err)
	}
	var owned []string
	for _, tb := range tables {
		cols, err := dao.ListColumns(ctx, raw, "", tb.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			if c.Name == store.WorkspaceColumn && tb.Name != "chunk_fts" {
				owned = append(owned, tb.Name)
			}
		}
	}
	if len(owned) < 10 {
		t.Fatalf("the catalog shows %d workspace-owned tables (%v): this test would show nothing", len(owned), owned)
	}
	err = s.Read(ctx, func(tx *store.Tx) error {
		have := accessors(NewStore(s, 1), tx)
		for _, name := range owned {
			if _, ok := have[name]; !ok {
				t.Errorf("table %s carries workspace_id and has no Scope accessor", name)
			}
		}
		for name, d := range have {
			if d.Name() != name {
				t.Errorf("the accessor for %s reaches %s", name, d.Name())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(owned)
	t.Logf("workspace-owned: %v", owned)
}

// seed writes one row into every workspace-owned table through sc.
func seed(t *testing.T, s *store.Store, x *Store) {
	t.Helper()
	err := s.Write(context.Background(), func(tx *store.Tx) error {
		doc, err := x.documents(tx).Set(DocPath, "n.md").Set(DocVersion, "v").Set(DocActiveGen, int64(1)).
			Set(DocIndexer, "i").Set(DocIndexedAt, int64(0)).Insert()
		if err != nil {
			return fmt.Errorf("document: %w", err)
		}
		if _, err := x.chunks(tx).Set(ChunkDoc, doc).Set(ChunkHash, []byte{1}).Set(ChunkTextHash, []byte{1}).
			Set(ChunkGenFrom, int64(1)).Set(ChunkOrd, int64(0)).Set(ChunkBreadcrumb, "").Set(ChunkBody, "zebra").
			Set(ChunkTitle, "").Set(ChunkTags, "").Set(ChunkByteStart, int64(0)).Set(ChunkByteEnd, int64(1)).Insert(); err != nil {
			return fmt.Errorf("chunk: %w", err)
		}
		if err := x.tagBatch(tx).Add(map[DocValueField]any{DocValueDoc: doc, DocValueValue: "t"}).Flush(); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
		if err := x.aliasBatch(tx).Add(map[DocValueField]any{DocValueDoc: doc, DocValueValue: "a"}).Flush(); err != nil {
			return fmt.Errorf("alias: %w", err)
		}
		if err := x.nameBatch(tx).Add(map[DocNameField]any{NameKey: "n", NameDoc: doc, NameIsPath: int64(1)}).Flush(); err != nil {
			return fmt.Errorf("name: %w", err)
		}
		if err := x.facetBatch(tx).Add(map[FacetField]any{FacetDoc: doc, FacetName: "status", FacetValue: "active"}).Flush(); err != nil {
			return fmt.Errorf("facet: %w", err)
		}
		if err := x.diagnosticBatch(tx).Add(map[DiagnosticField]any{DiagDoc: doc, DiagOrd: int64(0), DiagField: "type",
			DiagLine: int64(1), DiagRule: "required", DiagMessage: "type is required"}).Flush(); err != nil {
			return fmt.Errorf("diagnostic: %w", err)
		}
		link, err := x.linksOut(tx).Set(LinkSrc, doc).Set(LinkGenFrom, int64(1)).Set(LinkRaw, "[[x]]").
			Set(LinkName, "x").Set(LinkKind, "wikilink").Insert()
		if err != nil {
			return fmt.Errorf("link: %w", err)
		}
		if err := x.linkKeyBatch(tx).Add(map[LinkKeyField]any{LinkKeyLink: link, LinkKeyOrd: int64(0), LinkKeyKey: "x"}).Flush(); err != nil {
			return fmt.Errorf("link_key: %w", err)
		}
		if _, err := x.sc.Models(tx).Set(store.ModelFP, "m").Set(store.ModelActive, int64(1)).Insert(); err != nil {
			return fmt.Errorf("model: %w", err)
		}
		if err := x.embeddingBatch(tx).Add(map[EmbeddingField]any{EmbTextHash: []byte{1}, EmbModel: "m", EmbBits: []byte{0}, EmbF32: []byte{0}}).Flush(); err != nil {
			return fmt.Errorf("embedding: %w", err)
		}
		if _, err := x.jobs(tx).Set(JobPath, "n.md").Set(JobSeq, int64(1)).Insert(); err != nil {
			return fmt.Errorf("job: %w", err)
		}
		if _, err := x.changes(tx).Set(ChangeSeq, int64(1)).Set(ChangePath, "n.md").Set(ChangeOp, "upsert").
			Set(ChangeGeneration, int64(1)).Set(ChangeAt, int64(0)).Insert(); err != nil {
			return fmt.Errorf("change: %w", err)
		}
		if _, err := x.sc.Connections(tx).Set(store.ConnRole, store.RoleSource).Set(store.ConnEngine, store.EngineSQLite).Set(store.ConnDSN, []byte{1}).
			Set(store.ConnUpdatedAt, int64(0)).Insert(); err != nil {
			return fmt.Errorf("connection: %w", err)
		}
		if err := x.sc.GraphOnlyBatch(tx).Add(map[store.GraphOnlyField]any{store.GraphOnlyOrd: int64(0), store.GraphOnlyValue: "**/.todo-list/**"}).Flush(); err != nil {
			return fmt.Errorf("graph_only: %w", err)
		}
		return x.sc.PatternBatch(tx).Add(map[store.PatternField]any{store.PatternKind: "include", store.PatternOrd: int64(0), store.PatternValue: "*.md"}).Flush()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Through every accessor, workspace B sees none of A's rows, and B's updates
// and deletes change none of them; A's inserts landed as A's.
func TestEveryAccessorStaysInsideItsWorkspace(t *testing.T) {
	ctx := context.Background()
	s := openDB(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	A, B := NewStore(s, a.ID), NewStore(s, b.ID)
	seed(t, s, A)
	counts := func(x *Store) map[string]uint64 {
		t.Helper()
		out := map[string]uint64{}
		err := s.Read(ctx, func(tx *store.Tx) error {
			for name, d := range accessors(x, tx) {
				n, err := d.(interface{ Count() (uint64, error) }).Count()
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				out[name] = n
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for name, n := range counts(A) {
		if n != 1 {
			t.Errorf("A's %s has %d rows through A's scope, want its 1", name, n)
		}
	}
	for name, n := range counts(B) {
		if n != 0 {
			t.Errorf("B's scope sees %d of A's %s rows", n, name)
		}
	}
	err := s.Write(ctx, func(tx *store.Tx) error {
		for name, d := range accessors(B, tx) {
			n, err := d.(interface{ UpdateAffected() (int64, error) }).UpdateAffected()
			if err != nil {
				return fmt.Errorf("update %s: %w", name, err)
			}
			if n != 0 {
				t.Errorf("an update through B's scope changed %d of A's %s rows", n, name)
			}
		}
		for name, d := range accessors(B, tx) {
			if err := d.(interface{ Delete() error }).Delete(); err != nil {
				return fmt.Errorf("delete %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, n := range counts(A) {
		if n != 1 {
			t.Errorf("after B's deletes, A's %s has %d rows, want 1", name, n)
		}
	}
}

// Every declared join stays inside the workspace: A and B hold the same rows,
// the same text hash included, and B's vector has other bytes. Through A's
// scope, a join returns A's one row, never B's with it. On SQLite a document id
// is the store's own, so only the vector's join, keyed by text, could reach
// another workspace; the rest are checked as the same net.
func TestEveryJoinStaysInsideItsWorkspace(t *testing.T) {
	ctx := context.Background()
	s := openDB(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	A, B := NewStore(s, a.ID), NewStore(s, b.ID)
	seed(t, s, A)
	seed(t, s, B)
	if err := s.Write(ctx, func(tx *store.Tx) error {
		return B.embeddings(tx).Set(EmbBits, []byte{2}).Update()
	}); err != nil {
		t.Fatal(err)
	}
	err := s.Read(ctx, func(tx *store.Tx) error {
		vec, err := A.chunks(tx).Join(JoinEmbedding).Select(ChunkID, ChunkEmbBits)
		if err != nil {
			return err
		}
		if len(vec) != 1 || fmt.Sprint(vec[0].EmbBits) != "[0]" {
			var bits []string
			for _, c := range vec {
				bits = append(bits, fmt.Sprint(c.EmbBits))
			}
			t.Errorf("A's chunk joined to vectors %v, want only A's [0]", bits)
		}
		for name, n := range map[string]func() (int, error){
			"chunk→document": func() (int, error) {
				r, err := A.chunks(tx).Join(JoinDocument).Select(ChunkID, ChunkDocPath)
				return len(r), err
			},
			"doc_name→document": func() (int, error) {
				r, err := A.names(tx).Join(JoinDocument).Select(NameDoc, NameDocPath)
				return len(r), err
			},
			"link→target": func() (int, error) {
				r, err := A.linksOut(tx).Join(JoinOther).Select(LinkID, LinkOtherPath)
				return len(r), err
			},
			"link→source": func() (int, error) {
				r, err := A.linksIn(tx).Join(JoinOther).Select(LinkID, LinkOtherPath)
				return len(r), err
			},
		} {
			got, err := n()
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if got != 1 {
				t.Errorf("%s through A's scope: %d rows, want A's 1", name, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A document's facets and diagnostics are written and read through its workspace's scope.
func TestFacetsAndDiagnosticsAreTheWorkspaces(t *testing.T) {
	ctx := context.Background()
	s := openDB(t)
	w, err := s.AddWorkspace(ctx, "kb", "/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	x := NewStore(s, w.ID)
	err = s.Write(ctx, func(tx *store.Tx) error {
		doc, err := x.documents(tx).Set(DocPath, "n.md").Set(DocVersion, "v").Set(DocActiveGen, int64(1)).
			Set(DocIndexer, "i").Set(DocIndexedAt, int64(0)).Insert()
		if err != nil {
			return err
		}
		if err := x.facetBatch(tx).Add(map[FacetField]any{FacetDoc: doc, FacetName: "status", FacetValue: "active"}).Flush(); err != nil {
			return err
		}
		return x.diagnosticBatch(tx).Add(map[DiagnosticField]any{DiagDoc: doc, DiagOrd: int64(0), DiagField: "type",
			DiagLine: int64(2), DiagRule: "enum", DiagMessage: "m"}).Flush()
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *store.Tx) error {
		fs, err := x.facets(tx).Select()
		if err != nil || len(fs) != 1 || fs[0].WorkspaceID != w.ID || fs[0].Field != "status" || fs[0].Value != "active" || fs[0].DocID == 0 {
			t.Errorf("facet row = %+v, %v", fs, err)
		}
		ds, err := x.diagnostics(tx).Select()
		if err != nil || len(ds) != 1 || ds[0].WorkspaceID != w.ID || ds[0].Line != 2 || ds[0].Rule != "enum" || ds[0].Message != "m" || ds[0].Ord != 0 {
			t.Errorf("diagnostic row = %+v, %v", ds, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Identity is the identity a path was indexed under, and whether it is indexed.
func TestIdentityIsTheIndexedOnes(t *testing.T) {
	ctx := context.Background()
	s := openDB(t)
	w, err := s.AddWorkspace(ctx, "kb", "/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	x := NewStore(s, w.ID)
	if err := s.Write(ctx, func(tx *store.Tx) error {
		_, err := x.documents(tx).Set(DocPath, "n.md").Set(DocVersion, "v").Set(DocActiveGen, int64(1)).
			Set(DocIndexer, "i1").Set(DocIndexedAt, int64(0)).Insert()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := x.Identity(ctx, "n.md"); id != "i1" || !ok || err != nil {
		t.Errorf("an indexed path: %q %v %v", id, ok, err)
	}
	if id, ok, err := x.Identity(ctx, "gone.md"); id != "" || ok || err != nil {
		t.Errorf("a path not indexed: %q %v %v", id, ok, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok, err := x.Identity(cancelled, "n.md"); ok || err == nil {
		t.Errorf("a read that failed: %v %v", ok, err)
	}
}
