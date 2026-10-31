package rpc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/index"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// docsListed is index.documents' paths, in order, with its more and next.
func docsListed(t *testing.T, cli *golibrpc.Client, opts map[string]any) ([]string, bool, string, []any) {
	t.Helper()
	res := call(t, cli, "index.documents", "kb", opts).(map[string]any)
	var paths []string
	docs := res["docs"].([]any)
	for _, d := range docs {
		paths = append(paths, d.(map[string]any)["path"].(string))
	}
	return paths, res["more"].(bool), res["next"].(string), docs
}

// TestIndexDocumentsListsByUpdatedWithFrontmatter: the documents and their frontmatter, most recently
// updated first by default; narrowed by paths, tags and missing fields; by path when asked; a page
// at a time; an unknown order or option refused.
func TestIndexDocumentsListsByUpdatedWithFrontmatter(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	write := func(path, content string) {
		call(t, cli, "doc.write", "kb", path, []byte(content), "")
	}
	write("adrs/old.md", "---\ntype: adr\nstatus: accepted\nupdated: 2026-01-02\ntags: [kb]\nabstract: Old.\n---\n# Old\n")
	write("adrs/new.md", "---\ntype: adr\nstatus: proposed\nupdated: 2026-10-05\ntags: [kb, search]\n---\n# New\n")
	write("notes/mid.md", "---\ntype: note\nstatus: active\nupdated: 2026-05-01T10:00:00Z\nabstract: Mid.\n---\n# Mid\n")
	eventually(t, "the three documents indexed", func() bool {
		p, _, _, _ := docsListed(t, cli, nil)
		return len(p) == 3
	})

	paths, more, next, docs := docsListed(t, cli, nil)
	if !reflect.DeepEqual(paths, []string{"adrs/new.md", "notes/mid.md", "adrs/old.md"}) || more || next != "" {
		t.Errorf("by updated: %v more %v next %q", paths, more, next)
	}
	first := docs[0].(map[string]any)
	if first["title"] != "New" || first["updated"] != "2026-10-05" {
		t.Errorf("the first entry: %v", first)
	}
	if f := first["fields"].(map[string]any); f["type"] != "adr" || f["status"] != "proposed" {
		t.Errorf("the first entry's fields: %v", f)
	}
	for _, tc := range []struct {
		name string
		opts map[string]any
		want []string
	}{
		{"paths", map[string]any{"paths": []any{"adrs"}}, []string{"adrs/new.md", "adrs/old.md"}},
		{"tags", map[string]any{"tags": []any{"search"}}, []string{"adrs/new.md"}},
		{"missing", map[string]any{"missing": []any{"abstract"}}, []string{"adrs/new.md"}},
		{"by path", map[string]any{"sort": "path"}, []string{"adrs/new.md", "adrs/old.md", "notes/mid.md"}},
	} {
		if got, _, _, _ := docsListed(t, cli, tc.opts); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	page, more, next, _ := docsListed(t, cli, map[string]any{"limit": int64(2)})
	rest, more2, _, _ := docsListed(t, cli, map[string]any{"limit": int64(2), "after": next})
	if !reflect.DeepEqual(page, []string{"adrs/new.md", "notes/mid.md"}) || !more || !reflect.DeepEqual(rest, []string{"adrs/old.md"}) || more2 {
		t.Errorf("paging: %v more %v, then %v more %v", page, more, rest, more2)
	}
	if !strings.HasPrefix(next, "c1.") {
		t.Errorf("a page with more answered next %q, want an opaque cursor", next)
	}
	// a cursor is good for its own listing only: another sort, other filters
	for _, bad := range []map[string]any{{"limit": int64(2), "after": next, "sort": "path"},
		{"limit": int64(2), "after": next, "tags": []any{"kb"}}} {
		_, err := cli.Call(context.Background(), "index.documents", "kb", bad)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, "opts.after is not a cursor of this listing") {
			t.Errorf("index.documents %v: %v, want invalid params naming the cursor", bad, err)
		}
	}
	for _, bad := range []map[string]any{{"sort": "size"}, {"order": "path"}} {
		_, err := cli.Call(context.Background(), "index.documents", "kb", bad)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams {
			t.Errorf("index.documents %v: %v, want invalid params", bad, err)
		}
	}
}

// TestAnOlderSessionGetsTheCursor: from protocol 35, index.documents' next is an opaque cursor for
// every session, whatever protocol it declared (docs/decisions/search.md#cursors-not-offsets: the
// offset is retired at once), and a session that declared 33 gets a string next and has its integer
// after refused, the refusal saying what replaced it. 0 still starts the listing.
func TestAnOlderSessionGetsTheCursor(t *testing.T) {
	r := serve(t)
	writer := r.dial(true)
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		call(t, writer, "doc.write", "kb", p, []byte("# "+p+"\n"), "")
	}
	cli, _, err := helloAs(r, 33, "a-protocol-33-client")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the three documents indexed", func() bool {
		p, _, _, _ := docsListed(t, cli, map[string]any{"sort": "path"})
		return len(p) == 3
	})
	first, more, next, _ := docsListed(t, cli, map[string]any{"sort": "path", "limit": int64(2), "after": int64(0)})
	if !reflect.DeepEqual(first, []string{"a.md", "b.md"}) || !more || !strings.HasPrefix(next, "c1.") {
		t.Fatalf("a protocol-33 session's first page: %v more %v next %q, want a string cursor", first, more, next)
	}
	rest, more, next, _ := docsListed(t, cli, map[string]any{"sort": "path", "limit": int64(2), "after": next})
	if !reflect.DeepEqual(rest, []string{"c.md"}) || more || next != "" {
		t.Errorf("its second page: %v more %v next %q", rest, more, next)
	}
	_, err = cli.Call(context.Background(), "index.documents", "kb", map[string]any{"sort": "path", "after": int64(2)})
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, "an integer offset is no longer accepted") {
		t.Errorf("a protocol-33 session's integer after: %v, want invalid params saying the offset is retired", err)
	}
}

// TestASearchHitSaysItsLines: a hit on a Markdown file says the lines its span covers (the section's
// body: its heading is the breadcrumb), so a client reads that range alone; a hit whose span the
// file no longer holds says none.
func TestASearchHitSaysItsLines(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	content := "# Notes\n\nintro\n\n## Birds\n\nkestrel and plover\nmore birds\n"
	call(t, cli, "doc.write", "kb", "birds.md", []byte(content), "")
	var hit map[string]any
	eventually(t, "the section found", func() bool {
		hits := call(t, cli, "search.query", "kb", "kestrel").(map[string]any)["hits"].([]any)
		if len(hits) == 0 {
			return false
		}
		hit = hits[0].(map[string]any)
		return true
	})
	start, end := hit["byte_start"].(int64), hit["byte_end"].(int64)
	wantStart := int64(1 + countNL(content[:start]))
	wantEnd := wantStart + int64(countNL(content[start:end]))
	if end > start && content[end-1] == '\n' {
		wantEnd--
	}
	if hit["line_start"] != wantStart || hit["line_end"] != wantEnd || wantStart != 7 || wantEnd != 8 {
		t.Errorf("hit %v: lines %v..%v, want %d..%d", hit, hit["line_start"], hit["line_end"], wantStart, wantEnd)
	}
	// the file shrinks under the index: the span is past its end, so the hit says no lines
	if _, err := r.fsys.WriteFile(context.Background(), "birds.md", strings.NewReader("# Notes\n")); err != nil {
		t.Fatal(err)
	}
	hits := call(t, cli, "search.query", "kb", "kestrel").(map[string]any)["hits"].([]any)
	for _, h := range hits {
		m := h.(map[string]any)
		if m["path"] == "birds.md" && m["byte_end"].(int64) > int64(len("# Notes\n")) {
			if _, ok := m["line_start"]; ok {
				t.Errorf("a span past the file's end still says lines: %v", m)
			}
		}
	}
}

func countNL(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}

// TestSpanLines: the lines a byte span starts and ends on, a span ending with its newline ending on
// that line, and a span the content does not hold refused.
func TestSpanLines(t *testing.T) {
	content := []byte("one\ntwo\nthree\n")
	for _, tc := range []struct {
		start, end  int
		first, last int
		ok          bool
	}{
		{0, 3, 1, 1, true},  // "one"
		{0, 4, 1, 1, true},  // "one\n": ends on line 1
		{4, 13, 2, 3, true}, // "two\nthree"
		{4, 14, 2, 3, true}, // "two\nthree\n"
		{8, 8, 3, 3, true},  // empty, at "three"
		{0, 15, 0, 0, false},
		{5, 4, 0, 0, false},
	} {
		first, last, ok := spanLines(content, tc.start, tc.end)
		if first != tc.first || last != tc.last || ok != tc.ok {
			t.Errorf("spanLines(%d, %d) = %d, %d, %v; want %d, %d, %v", tc.start, tc.end, first, last, ok, tc.first, tc.last, tc.ok)
		}
	}
}

// TestDocumentsOptsReadsEveryOptionAndRefusesTheRest: index.documents' second parameter, each
// option read into its field, and each malformed value refused with the option it names.
func TestDocumentsOptsReadsEveryOptionAndRefusesTheRest(t *testing.T) {
	atNow := withProtocol(context.Background(), Protocol)
	o, err := documentsOpts(atNow, nil)
	if err != nil || o.Sort != "" || o.Limit != 0 {
		t.Fatalf("no opts: %+v, %v", o, err)
	}
	if _, err := documentsOpts(atNow, nil); err != nil {
		t.Fatalf("nil opts: %v", err)
	}
	o, err = documentsOpts(atNow, map[string]any{
		"sort": "path", "after": "c1.cursor", "limit": int64(5),
		"fields": []any{"status"}, "tags": []any{"adr"}, "paths": []any{"adrs/"}, "missing": []any{"abstract"}, "diagnosed": true,
		"facets": map[string]any{"status": "accepted", "type": []any{"adr", "note"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := index.DocumentsOpts{Sort: "path", After: "c1.cursor", Limit: 5, Fields: []string{"status"}, Tags: []string{"adr"},
		Paths: []string{"adrs/"}, Missing: []string{"abstract"}, Diagnosed: true,
		Facets: map[string][]string{"status": {"accepted"}, "type": {"adr", "note"}}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %+v\nwant %+v", o, want)
	}
	// below protocol 15 the option is unknown, true or false, as it is to a protocol-14 daemon
	at14 := withProtocol(context.Background(), 14)
	for _, v := range []bool{true, false} {
		_, err := documentsOpts(at14, map[string]any{"diagnosed": v})
		var rerr *golibrpc.Error
		if !errors.As(err, &rerr) || rerr.Code != golibrpc.CodeInvalidParams || !strings.Contains(rerr.Message, "needs protocol 15") {
			t.Errorf("diagnosed=%v at 14: %v, want invalid params naming protocol 15", v, err)
		}
	}
	for _, c := range []struct {
		opts any
		says string
	}{
		{"sort", "opts must be a map"},
		{map[string]any{"sort": int64(1)}, "opts.sort must be a string"},
		{map[string]any{"after": int64(-1)}, "opts.after is the previous page's next, an opaque cursor, since protocol 35"},
		{map[string]any{"after": int64(2)}, "an integer offset is no longer accepted"},
		{map[string]any{"after": true}, "opts.after is the previous page's next"},
		{map[string]any{"limit": "5"}, "opts.limit must be a non-negative integer"},
		{map[string]any{"fields": "status"}, "opts.fields must be a list of strings"},
		{map[string]any{"tags": []any{int64(1)}}, "opts.tags must be a list of strings"},
		{map[string]any{"paths": nil}, "opts.paths must be a list of strings"},
		{map[string]any{"missing": map[string]any{}}, "opts.missing must be a list of strings"},
		{map[string]any{"facets": []any{"status"}}, "opts.facets must be a map"},
		{map[string]any{"facets": map[string]any{"status": int64(1)}}, "opts.facets.status must be a list of strings"},
		{map[string]any{"diagnosed": "yes"}, "opts.diagnosed must be a boolean"},
		{map[string]any{"colour": "red"}, "unknown option colour"},
	} {
		_, err := documentsOpts(atNow, c.opts)
		var rerr *golibrpc.Error
		if !errors.As(err, &rerr) || rerr.Code != golibrpc.CodeInvalidParams || !strings.Contains(rerr.Message, c.says) {
			t.Errorf("opts %#v: got %v, want invalid params saying %q", c.opts, err, c.says)
		}
	}
}
