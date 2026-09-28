package index

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
	"github.com/yongjohnlee80/golib/parse/markdown"
)

// put writes and indexes notes, path then content.
func (e *env) put(pc ...string) {
	e.t.Helper()
	for i := 0; i < len(pc); i += 2 {
		e.write(pc[i], pc[i+1])
		e.ix.Touch(pc[i])
	}
	for i := 0; i < len(pc); i += 2 {
		e.indexedAt(pc[i])
	}
}

// remove deletes notes and waits for them to leave the index.
func (e *env) remove(ps ...string) {
	e.t.Helper()
	for _, p := range ps {
		if err := e.fsys.Remove(context.Background(), p); err != nil {
			e.t.Fatal(err)
		}
		e.ix.Touch(p)
	}
	for _, p := range ps {
		e.eventually(p+" gone", func() bool { _, ok := e.store.Version(p); return !ok })
	}
}

// targets lists a note's links as "raw -> path", "-" for unresolved.
func (e *env) targets(p string) []string {
	e.t.Helper()
	ls, err := e.store.Links(context.Background(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, l := range ls {
		to := l.Path
		if !l.Resolved {
			to = "-"
		}
		out = append(out, l.Raw+" -> "+to)
	}
	return out
}

func (e *env) unresolved() []string {
	e.t.Helper()
	us, err := e.store.Unresolved(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, u := range us {
		out = append(out, u.Src+" "+u.Raw+" "+u.Reason)
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestExtractLinks(t *testing.T) {
	src := "[[Plan]] [[notes/Plan.md#Goals|the goals]] ![[Diagram]] [[x#^b1]] [[#Local]] ![[pic.png]]\n" +
		"[[Meeting 2024.05.01]] `[[not a link]]` [[../shared/Conv]] [[./near#H]] [[../../../out]]\n\n" +
		"[md](other.md) [up](../top.md#Sec) [root](/r/deep.md) [enc](my%20note.md) [bare](plain)\n" +
		"[web](https://example.com/a.md) [mail](mailto:a@b.c) [self](#here) [out](../../gone.md)\n" +
		"[code](../scripts/run.sh) [img](pic.png) <https://auto.link>\n"
	doc := markdown.Parse([]byte(src), markdown.GFM(), markdown.Obsidian())
	var got []string
	for _, l := range extractLinks(doc, "dir/n.md", nil) {
		got = append(got, fmt.Sprintf("%s %s #%s %s", l.kind, l.name, l.anchor, l.raw))
	}
	eq(t, "links", got, []string{
		"wikilink plan # [[Plan]]",
		"wikilink notes/plan #Goals [[notes/Plan.md#Goals|the goals]]",
		"embed diagram # ![[Diagram]]",
		"wikilink x #^b1 [[x#^b1]]",
		"wikilink meeting 2024.05.01 # [[Meeting 2024.05.01]]",
		"wikilink /shared/conv # [[../shared/Conv]]",
		"wikilink /dir/near #H [[./near#H]]",
		"markdown dir/other.md # [md](other.md)",
		"markdown top.md #Sec [up](../top.md#Sec)",
		"markdown r/deep.md # [root](/r/deep.md)",
		"markdown dir/my note.md # [enc](my%20note.md)",
		"markdown dir/plain # [bare](plain)",
	})
	// an attachment type the workspace indexes is a note, judged by the workspace path it reaches
	var pdf []string
	for _, l := range extractLinks(markdown.Parse([]byte("![[doc.pdf]] [c](run.sh) ![[../assets/d.pdf]] [e](../assets/e.pdf) ![[../other/f.pdf]]"), markdown.Obsidian()), "notes/n.md",
		func(p string) bool {
			return p == "doc.pdf" || strings.HasSuffix(p, ".sh") || strings.HasPrefix(p, "assets/") && strings.HasSuffix(p, ".pdf")
		}) {
		pdf = append(pdf, l.name)
	}
	eq(t, "indexed attachment types", pdf, []string{"doc.pdf", "notes/run.sh", "/assets/d.pdf", "assets/e.pdf"})
}

// TestResolutionRules: each rule of ADR 0204 §4.6, and the markdown forms.
func TestResolutionRules(t *testing.T) {
	e := newEnv(t, Options{})
	e.put(
		"x.md", "root x", "d/x.md", "nested x",
		"d/y.md", "only y",
		"z.md", "---\naliases: [Zed, The Zee]\n---\nz",
		"a/w.md", "shallow w", "a/b/w.md", "deep w",
		"p/v.md", "v one", "q/v.md", "v two",
		"d/rel.md", "[[../x]] [[./x]] [[./y]] [[./w]] [[../a/b/w]]",
		"src.md", "[[x]] [[X]] [[y]] [[zed]] [[the zee]] [[w]] [[b/w]] [[a/b/w]] [[v]] [[nothere]] [[src]]\n\n"+
			"[m](d/y.md) [n](d/y) [o](d/none.md)\n",
	)
	eq(t, "links", e.targets("src.md"), []string{
		"[[x]] -> x.md", // the exact path, over d/x.md's file name
		"[[X]] -> x.md", // names compare case-insensitively
		"[[y]] -> d/y.md",
		"[[zed]] -> z.md", "[[the zee]] -> z.md",
		"[[w]] -> a/w.md", // of two file names, the one nearer the root
		"[[b/w]] -> a/b/w.md",
		"[[a/b/w]] -> a/b/w.md",
		"[[v]] -> -", // tied
		"[[nothere]] -> -",
		"[[src]] -> src.md",
		"[m](d/y.md) -> d/y.md", "[n](d/y) -> d/y.md", "[o](d/none.md) -> -",
	})
	// a relative path names one path: d/rel.md's [[./x]] is d/x.md, though x.md is at the root
	// and never a file name elsewhere: [[./w]] is d/w.md, which does not exist
	eq(t, "relative", e.targets("d/rel.md"), []string{"[[../x]] -> x.md", "[[./x]] -> d/x.md", "[[./y]] -> d/y.md",
		"[[./w]] -> -", "[[../a/b/w]] -> a/b/w.md"})
	eq(t, "unresolved", e.unresolved(), []string{
		"d/rel.md [[./w]] missing",
		"src.md [[v]] ambiguous", "src.md [[nothere]] missing", "src.md [o](d/none.md) missing",
	})
}

// TestExactFileTakesOverFromAlias: a new x.md takes [[x]] from an alias match, and gives it back
// when it goes.
func TestExactFileTakesOverFromAlias(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("n.md", "---\naliases: [x]\n---\nn", "src.md", "[[x]]")
	eq(t, "before", e.targets("src.md"), []string{"[[x]] -> n.md"})
	e.put("x.md", "the real x")
	eq(t, "x.md created", e.targets("src.md"), []string{"[[x]] -> x.md"})
	e.remove("x.md")
	eq(t, "x.md deleted", e.targets("src.md"), []string{"[[x]] -> n.md"})
}

// TestRenameReresolves: renaming a target unresolves the links to its old name and resolves those
// to its new one, markdown and relative links too.
func TestRenameReresolves(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("old.md", "target", "a.md", "[[old]] [p](old.md) [[./old]]", "b.md", "[[new]] [q](new.md) [[./new]]")
	eq(t, "a before", e.targets("a.md"), []string{"[[old]] -> old.md", "[p](old.md) -> old.md", "[[./old]] -> old.md"})
	eq(t, "b before", e.targets("b.md"), []string{"[[new]] -> -", "[q](new.md) -> -", "[[./new]] -> -"})
	if err := e.fsys.Rename(context.Background(), "old.md", "new.md"); err != nil {
		t.Fatal(err)
	}
	e.ix.Touch("old.md")
	e.ix.Touch("new.md")
	e.indexedAt("new.md")
	e.eventually("old.md gone", func() bool { _, ok := e.store.Version("old.md"); return !ok })
	eq(t, "a after", e.targets("a.md"), []string{"[[old]] -> -", "[p](old.md) -> -", "[[./old]] -> -"})
	eq(t, "b after", e.targets("b.md"), []string{"[[new]] -> new.md", "[q](new.md) -> new.md", "[[./new]] -> new.md"})
}

// TestDeletedTargetUnresolves: deleting a target leaves its links unresolved, and deleting a
// source takes its links with it.
func TestDeletedTargetUnresolves(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("t.md", "target", "s.md", "[[t]] ![[t#Part]] [m](t.md)")
	e.remove("t.md")
	eq(t, "links", e.targets("s.md"), []string{"[[t]] -> -", "![[t#Part]] -> -", "[m](t.md) -> -"})
	eq(t, "unresolved", e.unresolved(), []string{"s.md [[t]] missing", "s.md ![[t#Part]] missing", "s.md [m](t.md) missing"})
	e.remove("s.md")
	eq(t, "unresolved after the source went", e.unresolved(), nil)
}

// TestAliasChangeReresolves: an alias gained takes the links under it; an alias dropped lets them
// go, to another note or to none.
func TestAliasChangeReresolves(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a/k.md", "an older k", "n.md", "no alias yet", "src.md", "[[k]] [[nick]]")
	eq(t, "before", e.targets("src.md"), []string{"[[k]] -> a/k.md", "[[nick]] -> -"})
	e.put("n.md", "---\naliases: [k, nick]\n---\nnow both")
	eq(t, "aliases added", e.targets("src.md"), []string{"[[k]] -> n.md", "[[nick]] -> n.md"})
	e.put("n.md", "aliases gone")
	eq(t, "aliases dropped", e.targets("src.md"), []string{"[[k]] -> a/k.md", "[[nick]] -> -"})
}

// TestBacklinksMatchLinks: every resolved link is its target's backlink, and nothing else is.
func TestBacklinksMatchLinks(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "[[b]] [[c]] [[a]]", "b.md", "[[c]] [x](a.md) [[missing]]", "c.md", "![[b#H]]", "d.md", "none")
	ctx := context.Background()
	var fromLinks, fromBacklinks []string
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md"} {
		ls, err := e.store.Links(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range ls {
			if l.Resolved {
				fromLinks = append(fromLinks, p+" "+l.Raw+" "+l.Path+" "+l.Kind+" "+l.Anchor)
			}
		}
		bs, err := e.store.Backlinks(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bs {
			fromBacklinks = append(fromBacklinks, b.Path+" "+b.Raw+" "+p+" "+b.Kind+" "+b.Anchor)
		}
	}
	sort.Strings(fromLinks)
	sort.Strings(fromBacklinks)
	eq(t, "backlinks against links", fromBacklinks, fromLinks)
	if len(fromLinks) != 6 {
		t.Errorf("%d resolved links, want 6: %q", len(fromLinks), fromLinks)
	}
	if _, err := e.store.Links(ctx, "nope.md"); !errors.Is(err, ErrNoDocument) {
		t.Errorf("links of an unknown path: %v", err)
	}
}

func TestNeighborhood(t *testing.T) {
	e := newEnv(t, Options{})
	// a → b → c → d, e → a, and f alone
	e.put("a.md", "[[b]]", "b.md", "[[c]]", "c.md", "[[d]]", "d.md", "end", "e.md", "![[a]]", "f.md", "[[nothing]]")
	ctx := context.Background()
	for depth, want := range map[int]Neighborhood{
		1: {Nodes: []string{"a.md", "b.md", "e.md"},
			Edges: []Edge{{"a.md", "b.md", LinkWikilink}, {"e.md", "a.md", LinkEmbed}}},
		2: {Nodes: []string{"a.md", "b.md", "c.md", "e.md"},
			Edges: []Edge{{"a.md", "b.md", LinkWikilink}, {"b.md", "c.md", LinkWikilink}, {"e.md", "a.md", LinkEmbed}}},
		9: {Nodes: []string{"a.md", "b.md", "c.md", "e.md"}, // clamped to 2
			Edges: []Edge{{"a.md", "b.md", LinkWikilink}, {"b.md", "c.md", LinkWikilink}, {"e.md", "a.md", LinkEmbed}}},
	} {
		got, err := e.store.Neighborhood(ctx, "a.md", depth)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("depth %d:\n got %+v\nwant %+v", depth, got, want)
		}
	}
	got, err := e.store.Neighborhood(ctx, "f.md", 2)
	if err != nil || !reflect.DeepEqual(got, Neighborhood{Nodes: []string{"f.md"}}) {
		t.Errorf("a lone note: %+v, %v", got, err)
	}
}

// TestSchemaOneGainsLinks: a store written under schema 1, which kept no links or names, is
// migrated in place, and every document is rebuilt with its links though no file changed.
func TestSchemaOneGainsLinks(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "[[b]]", "b.md", "[[a]] [x](a.md)")
	want := [][]string{e.targets("a.md"), e.targets("b.md")}
	e.stop()
	path := filepath.Join(e.dir, "index.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE meta SET v = '1' WHERE k = 'schema_version'", "DELETE FROM link", "DROP TABLE doc_name",
		"DROP INDEX link_src", "DROP INDEX link_dst", "UPDATE document SET indexer = 'c1.s1'"} {
		if _, err := s.w.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	e.stop = nil
	e.open(Options{})
	e.eventually("both documents rebuilt", func() bool {
		var stale int
		_ = scanOne(context.Background(), e.store.r, &stale, "SELECT COUNT(*) FROM document WHERE indexer != ?", IndexerVersion)
		return stale == 0
	})
	eq(t, "a.md", e.targets("a.md"), want[0])
	eq(t, "b.md", e.targets("b.md"), want[1])
	var v string
	_ = scanOne(context.Background(), e.store.r, &v, "SELECT v FROM meta WHERE k = 'schema_version'")
	if v != "2" {
		t.Errorf("schema_version %q after the migration, want 2", v)
	}
}

// TestNewerSchemaIsLeftAlone: a file of a newer schema is refused before anything in it changes.
func TestNewerSchemaIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE meta SET v = '3' WHERE k = 'schema_version'", "DROP TABLE doc_name"} {
		if _, err := s.w.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	if s, err := Open(context.Background(), path); !errors.Is(err, ErrSchemaVersion) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("opening a schema 3 store: %v, want ErrSchemaVersion", err)
	}
	// look at the file without Open: it must not have gained this schema's tables
	raw, err := openRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var n int
	if err := scanOne(context.Background(), raw, &n, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'doc_name'"); err != nil || n != 0 {
		t.Errorf("the refused file gained doc_name (%d, %v)", n, err)
	}
}

// openRaw opens an index file as SQLite alone, with no migration.
func openRaw(path string) (dao.DataConn, error) {
	return sqlite.OpenNamed(context.Background(), "raw:"+path, "file:"+path, sqlite.MaxOpenConns(1))
}

// TestTieBrokenByDelete: of two notes tied for a name, deleting one gives the link to the other,
// though the link never reached the one deleted.
func TestTieBrokenByDelete(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("p/v.md", "one", "q/v.md", "two", "src.md", "[[v]]")
	eq(t, "tied", e.targets("src.md"), []string{"[[v]] -> -"})
	e.remove("p/v.md")
	eq(t, "one left", e.targets("src.md"), []string{"[[v]] -> q/v.md"})
}
