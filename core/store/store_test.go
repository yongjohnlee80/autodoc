package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_AppliesTheSchemaAndHoldsTheLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Schema(); len(st.Pending) == 0 || st.Pending[0] != "000001_update_initialize_tables.sql" {
		t.Errorf("a new store's schema update = %+v, want the baseline applied", st)
	}
	if _, err := Open(ctx, path); !errors.Is(err, ErrBusy) {
		t.Errorf("a second Open of a served store = %v, want ErrBusy", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if st := s2.Schema(); len(st.Pending) != 0 || len(st.Applied) == 0 {
		t.Errorf("a reopened store's schema update = %+v, want nothing pending", st)
	}
}

func TestSectionSizeBelongsToOneWorkspace(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, err := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.SectionTokens(ctx, a.ID); err != nil || n != SectionTokensDefault {
		t.Fatalf("new workspace: %d, %v", n, err)
	}
	if err := s.SetWorkspaceSectionTokens(ctx, a.ID, 256); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   int64
		want int
	}{{a.ID, 256}, {b.ID, SectionTokensDefault}} {
		got, err := s.SectionTokens(ctx, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("workspace %d: %d, %v; want %d", tc.id, got, err, tc.want)
		}
	}
	if err := s.SetWorkspaceSectionTokens(ctx, a.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SectionTokens(ctx, a.ID); got != SectionTokensDefault {
		t.Errorf("reset to default = %d", got)
	}
	for _, size := range []int{-1, 127, 2049} {
		if err := s.SetWorkspaceSectionTokens(ctx, a.ID, size); err == nil {
			t.Errorf("section size %d was accepted", size)
		}
	}
	if err := s.SetWorkspaceSectionTokens(ctx, 99999, 256); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown workspace update: %v", err)
	}
	if _, err := s.SectionTokens(ctx, 99999); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown workspace lookup: %v", err)
	}
}

func TestWorkspaceEmbeddingPolicyDefaultsAndIsScoped(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, err := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.EmbeddingPolicy(ctx, a.ID); err != nil || got != EmbeddingAlways {
		t.Fatalf("default = %q, %v", got, err)
	}
	if err := s.SetWorkspaceEmbeddingPolicy(ctx, a.ID, EmbeddingWhenOpened); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   int64
		want string
	}{{a.ID, EmbeddingWhenOpened}, {b.ID, EmbeddingAlways}} {
		if got, err := s.EmbeddingPolicy(ctx, c.id); err != nil || got != c.want {
			t.Errorf("workspace %d: %q, %v; want %q", c.id, got, err, c.want)
		}
	}
	if err := s.SetWorkspaceEmbeddingPolicy(ctx, a.ID, "sometimes"); !errors.Is(err, ErrEmbeddingPolicy) {
		t.Errorf("invalid policy: %v", err)
	}
	if err := s.SetWorkspaceEmbeddingPolicy(ctx, 99999, EmbeddingNever); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := s.EmbeddingPolicy(ctx, 99999); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown workspace policy lookup: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.EmbeddingPolicy(cancelled, a.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled policy lookup: %v", err)
	}
	if err := s.SetWorkspaceEmbeddingPolicy(cancelled, a.ID, EmbeddingNever); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled policy update: %v", err)
	}
	if got, err := s.EmbeddingPolicy(ctx, a.ID); err != nil || got != EmbeddingWhenOpened {
		t.Errorf("cancelled update changed policy to %q: %v", got, err)
	}
}

// The order the daemon runs on darwin: SQLite already holds its fcntl locks on
// the store when the lease is taken. A lease on the store file itself is
// refused there; the sidecar is not.
func TestLease_AcquiresOverAnOpenStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	db, err := sqlite.Open(ctx, "file:"+path+"?_pragma=journal_mode(WAL)", sqlite.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE TABLE x (a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	f, err := acquireLease(path)
	if err != nil {
		t.Fatalf("the lease was refused over an open store: %v", err)
	}
	if _, err := acquireLease(path); !errors.Is(err, ErrBusy) {
		t.Errorf("a second lease = %v, want ErrBusy", err)
	}
	_ = f.Close()
	if _, err := db.ExecContext(ctx, "INSERT INTO x VALUES (1)"); err != nil {
		t.Errorf("releasing the lease disturbed the store: %v", err)
	}
}

// A store reached through a symlinked directory is the same store, so its lease is the same
// lease: the sidecar names the store's device and inode, not the path.
func TestLease_NamesTheStoreNotThePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, filepath.Join(alias, "autodoc.db")); !errors.Is(err, ErrBusy) {
		t.Fatalf("Open through a symlinked directory = %v, want ErrBusy", err)
	}
}

func TestWorkspaces_AddListRenameRemove(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, err := s.AddWorkspace(ctx, "kb", "/kb", []string{"**/*.md"}, []string{".git/**", "tmp/**"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWorkspace(ctx, "kb", "/other", nil, nil); !errors.Is(err, ErrTaken) {
		t.Errorf("a taken name = %v, want ErrTaken", err)
	}
	if _, err := s.AddWorkspace(ctx, "other", "/kb", nil, nil); !errors.Is(err, ErrTaken) {
		t.Errorf("a taken root = %v, want ErrTaken", err)
	}
	b, err := s.AddWorkspace(ctx, "notes", "/notes", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := s.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Name != "kb" || fmt.Sprint(ws[0].Include, ws[0].Exclude) != "[**/*.md] [.git/** tmp/**]" || ws[1].Name != "notes" {
		t.Errorf("Workspaces = %+v", ws)
	}
	if err := s.RenameWorkspace(ctx, b.ID, "kb"); !errors.Is(err, ErrTaken) {
		t.Errorf("a rename onto a taken name = %v, want ErrTaken", err)
	}
	if err := s.RenameWorkspace(ctx, a.ID, "knowledge"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameWorkspace(ctx, 999, "x"); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("renaming no workspace = %v, want ErrNoWorkspace", err)
	}
	if err := s.RemoveWorkspace(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveWorkspace(ctx, a.ID); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("removing it twice = %v, want ErrNoWorkspace", err)
	}
	ws, _ = s.Workspaces(ctx)
	if len(ws) != 1 || ws[0].Name != "notes" {
		t.Errorf("after the remove: %+v", ws)
	}
}

// accessors is every Scope accessor, by the table it reaches.
func accessors(sc *Scope, tx *Tx) map[string]dao.Named {
	return map[string]dao.Named{
		"workspace_pattern": sc.Patterns(tx), "document": sc.Documents(tx), "chunk": sc.Chunks(tx),
		"doc_tag": sc.Tags(tx), "doc_alias": sc.Aliases(tx), "doc_name": sc.Names(tx),
		"doc_facet": sc.Facets(tx), "doc_diagnostic": sc.Diagnostics(tx),
		"link": sc.LinksOut(tx), "model": sc.Models(tx), "embedding": sc.Embeddings(tx),
		"index_job": sc.Jobs(tx), "change": sc.Changes(tx),
	}
}

// Every table the live store has with a workspace_id column has a Scope
// accessor: read from the store's catalog, not from the declarations, so a new
// table a script adds is caught when it has none. chunk_fts is the full-text
// index, reached only through Chunks' join.
func TestEveryWorkspaceOwnedTableHasAScopeAccessor(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	tables, err := dao.ListTables(ctx, s.r, "")
	if err != nil {
		t.Fatal(err)
	}
	var owned []string
	for _, tb := range tables {
		cols, err := dao.ListColumns(ctx, s.r, "", tb.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			if c.Name == wsCol && tb.Name != "chunk_fts" {
				owned = append(owned, tb.Name)
			}
		}
	}
	if len(owned) < 10 {
		t.Fatalf("the catalog shows %d workspace-owned tables (%v): this test would show nothing", len(owned), owned)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		have := accessors(s.Workspace(1), tx)
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
func seed(t *testing.T, s *Store, sc *Scope) {
	t.Helper()
	err := s.Write(context.Background(), func(tx *Tx) error {
		doc, err := sc.Documents(tx).Set(DocPath, "n.md").Set(DocVersion, "v").Set(DocActiveGen, int64(1)).
			Set(DocIndexer, "i").Set(DocIndexedAt, int64(0)).Insert()
		if err != nil {
			return fmt.Errorf("document: %w", err)
		}
		if _, err := sc.Chunks(tx).Set(ChunkDoc, doc).Set(ChunkHash, []byte{1}).Set(ChunkTextHash, []byte{1}).
			Set(ChunkGenFrom, int64(1)).Set(ChunkOrd, int64(0)).Set(ChunkBreadcrumb, "").Set(ChunkBody, "zebra").
			Set(ChunkTitle, "").Set(ChunkTags, "").Set(ChunkByteStart, int64(0)).Set(ChunkByteEnd, int64(1)).Insert(); err != nil {
			return fmt.Errorf("chunk: %w", err)
		}
		if err := sc.TagBatch(tx).Add(map[DocValueField]any{DocValueDoc: doc, DocValueValue: "t"}).Flush(); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
		if err := sc.AliasBatch(tx).Add(map[DocValueField]any{DocValueDoc: doc, DocValueValue: "a"}).Flush(); err != nil {
			return fmt.Errorf("alias: %w", err)
		}
		if err := sc.NameBatch(tx).Add(map[DocNameField]any{NameKey: "n", NameDoc: doc, NameIsPath: int64(1)}).Flush(); err != nil {
			return fmt.Errorf("name: %w", err)
		}
		if err := sc.FacetBatch(tx).Add(map[FacetField]any{FacetDoc: doc, FacetName: "status", FacetValue: "active"}).Flush(); err != nil {
			return fmt.Errorf("facet: %w", err)
		}
		if err := sc.DiagnosticBatch(tx).Add(map[DiagnosticField]any{DiagDoc: doc, DiagOrd: int64(0), DiagField: "type",
			DiagLine: int64(1), DiagRule: "required", DiagMessage: "type is required"}).Flush(); err != nil {
			return fmt.Errorf("diagnostic: %w", err)
		}
		if _, err := sc.LinksOut(tx).Set(LinkSrc, doc).Set(LinkGenFrom, int64(1)).Set(LinkRaw, "[[x]]").
			Set(LinkName, "x").Set(LinkKind, "wikilink").Insert(); err != nil {
			return fmt.Errorf("link: %w", err)
		}
		if _, err := sc.Models(tx).Set(ModelFP, "m").Set(ModelActive, int64(1)).Insert(); err != nil {
			return fmt.Errorf("model: %w", err)
		}
		if err := sc.EmbeddingBatch(tx).Add(map[EmbeddingField]any{EmbTextHash: []byte{1}, EmbModel: "m", EmbBits: []byte{0}, EmbF32: []byte{0}}).Flush(); err != nil {
			return fmt.Errorf("embedding: %w", err)
		}
		if _, err := sc.Jobs(tx).Set(JobPath, "n.md").Set(JobSeq, int64(1)).Insert(); err != nil {
			return fmt.Errorf("job: %w", err)
		}
		if _, err := sc.Changes(tx).Set(ChangeSeq, int64(1)).Set(ChangePath, "n.md").Set(ChangeOp, "upsert").
			Set(ChangeGeneration, int64(1)).Set(ChangeAt, int64(0)).Insert(); err != nil {
			return fmt.Errorf("change: %w", err)
		}
		return sc.PatternBatch(tx).Add(map[PatternField]any{PatternKind: "include", PatternOrd: int64(0), PatternValue: "*.md"}).Flush()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Through every accessor, workspace B sees none of A's rows, and B's updates
// and deletes change none of them; A's inserts landed as A's.
func TestEveryAccessorStaysInsideItsWorkspace(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	A, B := s.Workspace(a.ID), s.Workspace(b.ID)
	seed(t, s, A)
	counts := func(sc *Scope) map[string]uint64 {
		t.Helper()
		out := map[string]uint64{}
		err := s.Read(ctx, func(tx *Tx) error {
			for name, d := range accessors(sc, tx) {
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
	err := s.Write(ctx, func(tx *Tx) error {
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
	s := openStore(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	A, B := s.Workspace(a.ID), s.Workspace(b.ID)
	seed(t, s, A)
	seed(t, s, B)
	if err := s.Write(ctx, func(tx *Tx) error {
		return B.Embeddings(tx).Set(EmbBits, []byte{2}).Update()
	}); err != nil {
		t.Fatal(err)
	}
	err := s.Read(ctx, func(tx *Tx) error {
		vec, err := A.Chunks(tx).Join(JoinEmbedding).Select(ChunkID, ChunkEmbBits)
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
				r, err := A.Chunks(tx).Join(JoinDocument).Select(ChunkID, ChunkDocPath)
				return len(r), err
			},
			"doc_name→document": func() (int, error) {
				r, err := A.Names(tx).Join(JoinDocument).Select(NameDoc, NameDocPath)
				return len(r), err
			},
			"link→target": func() (int, error) {
				r, err := A.LinksOut(tx).Join(JoinOther).Select(LinkID, LinkOtherPath)
				return len(r), err
			},
			"link→source": func() (int, error) {
				r, err := A.LinksIn(tx).Join(JoinOther).Select(LinkID, LinkOtherPath)
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

// Preferences are kept by name, the latest value winning, and read back
// after the store is opened again; a preference needs a name.
func TestPreferencesAreKeptByName(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"tui.theme", "light"}, {"tui.ruler", "120"}, {"tui.theme", "retro"}} {
		if err := s.SetPreference(ctx, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPreference(ctx, "", "x"); !errors.Is(err, ErrNoPreferenceName) {
		t.Fatalf("a nameless preference = %v, want ErrNoPreferenceName", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Preferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "map[tui.ruler:120 tui.theme:retro]" {
		t.Fatalf("preferences after reopening: %v", got)
	}
}
