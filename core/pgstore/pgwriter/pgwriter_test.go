package pgwriter_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/index/writertest"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgwriter"
)

// pgstore's writer conforms, deferring what it declares.
func TestThePostgresWriterConforms(t *testing.T) {
	writertest.Run(t, writertest.Harness{Defers: pgwriter.Deferred,
		New: func(t *testing.T) (index.Writer, func(context.Context, string) (writertest.Kept, bool, error)) {
			conn := pgtest.Scratch(t, pgstore.Migrate)
			s, err := pgstore.New(conn, "tenant")
			if err != nil {
				t.Fatal(err)
			}
			return pgwriter.New(s), func(ctx context.Context, path string) (writertest.Kept, bool, error) {
				return readKept(ctx, conn, path)
			}
		}})
}

// A chunk's kind reaches pgstore: the abstract is kept as one, so a sections-only search leaves it
// out. The mutant, named here: document() not passing the kinds keeps every chunk a section.
func TestTheWriterKeepsEachChunksKind(t *testing.T) {
	ctx := context.Background()
	conn := pgtest.Scratch(t, pgstore.Migrate)
	s, err := pgstore.New(conn, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	b, err := pgwriter.New(s).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sec := search.Chunk{Ord: 0, Breadcrumb: "a", Body: "a section"}
	abs := search.Chunk{Ord: 1, Breadcrumb: "a", Body: "the abstract"}
	err = b.Put(ctx, index.Doc{Path: "a.md", Chunks: []index.DocChunk{{Chunk: sec, Kind: index.ChunkSection}, {Chunk: abs, Kind: index.ChunkAbstract}}})
	if err != nil {
		_ = b.Rollback()
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(ctx, "SELECT string_agg(kind, ',' ORDER BY ord) FROM rag_chunk WHERE tenant = 'tenant'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got string
	if !rows.Next() || rows.Scan(&got) != nil || got != "section,abstract" {
		t.Errorf("the kinds kept: %q, want section,abstract", got)
	}
}

// readKept reads back what pgstore keeps of path, with SQL of the test's own.
func readKept(ctx context.Context, conn dao.DataConn, path string) (writertest.Kept, bool, error) {
	var k writertest.Kept
	var id int64
	var tags, facets string
	rows, err := conn.QueryContext(ctx, `SELECT id, version, indexer, title, array_to_string(tags, E'\x1f'),
		array_to_string(facets, E'\x1f') FROM rag_document WHERE tenant = 'tenant' AND path = $1`, path)
	if err != nil {
		return k, false, err
	}
	found := rows.Next()
	if found {
		err = rows.Scan(&id, &k.Version, &k.Indexer, &k.Title, &tags, &facets)
	}
	_ = rows.Close()
	if err != nil || !found {
		return k, false, err
	}
	if tags != "" {
		k.Tags = strings.Split(tags, "\x1f")
	}
	for _, pair := range strings.Split(facets, "\x1f") {
		if f, v, ok := strings.Cut(pair, "="); ok {
			if k.Facets == nil {
				k.Facets = map[string][]string{}
			}
			k.Facets[f] = append(k.Facets[f], v)
		}
	}
	links, err := conn.QueryContext(ctx, `SELECT dst_path FROM rag_link WHERE tenant = 'tenant' AND src = $1 ORDER BY dst_path`, id)
	if err != nil {
		return k, false, err
	}
	for links.Next() {
		var l string
		if err := links.Scan(&l); err != nil {
			return k, false, err
		}
		k.Links = append(k.Links, l)
	}
	_ = links.Close()
	chunks, err := conn.QueryContext(ctx, `SELECT breadcrumb, body, text_hash FROM rag_chunk WHERE tenant = 'tenant' AND doc = $1 ORDER BY ord`, id)
	if err != nil {
		return k, false, err
	}
	defer chunks.Close()
	for chunks.Next() {
		var c writertest.KeptChunk
		if err := chunks.Scan(&c.Breadcrumb, &c.Body, &c.TextHash); err != nil {
			return k, false, err
		}
		k.Chunks = append(k.Chunks, c)
	}
	return k, true, chunks.Err()
}

// This package joins core/index and core/pgstore, so neither imports the other.
func TestNeitherIndexNorPgstoreImportsTheOther(t *testing.T) {
	deps := func(pkg string) string {
		t.Helper()
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Skipf("go list %s: %v", pkg, err)
		}
		return string(out)
	}
	const mod = "github.com/yongjohnlee80/autodoc/"
	if strings.Contains(deps(mod+"core/pgstore"), mod+"core/index\n") {
		t.Error("core/pgstore imports core/index")
	}
	if strings.Contains(deps(mod+"core/index"), mod+"core/pgstore\n") {
		t.Error("core/index imports core/pgstore")
	}
}
