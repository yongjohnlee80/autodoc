package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// unresolvedRig serves kb with one document whose five links name nothing, indexed, and a session
// at this build's protocol and one at 45, before graph.unresolved came in pages.
func unresolvedRig(t *testing.T) (cur, old *golibrpc.Client) {
	t.Helper()
	r := serve(t)
	if _, err := r.fsys.WriteFile(context.Background(), "a.md",
		strings.NewReader("# A\n\n[[gone-1]] [[gone-2]] [[gone-3]] [[gone-4]] [[gone-5]]\n")); err != nil {
		t.Fatal(err)
	}
	var err error
	if cur, _, err = helloAs(r, Protocol, "a-new-client"); err != nil {
		t.Fatal(err)
	}
	if old, _, err = helloAs(r, 45, "an-old-client"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a.md's five links indexed", func() bool {
		res, err := old.Call(context.Background(), "graph.unresolved", "kb")
		list, _ := res.([]any)
		return err == nil && len(list) == 5
	})
	return cur, old
}

// raws is the raw of each unresolved row in list.
func raws(list []any) []string {
	var out []string
	for _, u := range list {
		out = append(out, u.(map[string]any)["raw"].(string))
	}
	return out
}

// TestUnresolvedComesInPages: from protocol 50, graph.unresolved answers a page: opts.limit rows
// after opts.offset, the total count, and next, the offset of the next page, absent on the last.
// Read page by page, it is the whole list a session at 45 gets in one reply, in its order. With
// no opts, a page of the default size.
func TestUnresolvedComesInPages(t *testing.T) {
	cur, old := unresolvedRig(t)
	whole := raws(call(t, old, "graph.unresolved", "kb").([]any))

	var paged []string
	offset := int64(0)
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatalf("more than three pages of two for five rows: %v", paged)
		}
		res := call(t, cur, "graph.unresolved", "kb", map[string]any{"limit": int64(2), "offset": offset}).(map[string]any)
		rows := res["unresolved"].([]any)
		if res["count"] != int64(5) {
			t.Errorf("page at %d: count %v, want the total, 5", offset, res["count"])
		}
		if len(rows) > 2 {
			t.Errorf("page at %d: %d rows, want at most the limit, 2", offset, len(rows))
		}
		paged = append(paged, raws(rows)...)
		next, more := res["next"]
		if !more {
			if offset != 4 || len(rows) != 1 {
				t.Errorf("the last page is at %d with %d rows, want at 4 with 1", offset, len(rows))
			}
			break
		}
		if next != offset+2 {
			t.Errorf("page at %d: next %v, want %d", offset, next, offset+2)
		}
		offset = next.(int64)
	}
	if !reflect.DeepEqual(paged, whole) {
		t.Errorf("the pages read %v, the whole list is %v", paged, whole)
	}

	res := call(t, cur, "graph.unresolved", "kb").(map[string]any)
	if _, more := res["next"]; more || res["count"] != int64(5) || !reflect.DeepEqual(raws(res["unresolved"].([]any)), whole) {
		t.Errorf("with no opts: %v, want every row on one page of the default size", res)
	}
	res = call(t, cur, "graph.unresolved", "kb", map[string]any{"offset": int64(9)}).(map[string]any)
	if _, more := res["next"]; more || res["count"] != int64(5) || len(res["unresolved"].([]any)) != 0 {
		t.Errorf("an offset past the end: %v, want no rows, the count, no next", res)
	}
}

// TestUnresolvedPagesAreBounded: a limit of 1 to 2000 is taken, anything else refused, as is a
// negative offset; a session at 45 keeps the one reply, and is refused limit and offset.
func TestUnresolvedPagesAreBounded(t *testing.T) {
	cur, old := unresolvedRig(t)
	refused := func(c *golibrpc.Client, opts map[string]any, want string) {
		t.Helper()
		_, err := c.Call(context.Background(), "graph.unresolved", "kb", opts)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, want) {
			t.Errorf("opts %v: %v, want invalid params naming %q", opts, err, want)
		}
	}
	for _, limit := range []int64{0, -1, 2001} {
		refused(cur, map[string]any{"limit": limit}, "opts.limit")
	}
	refused(cur, map[string]any{"offset": int64(-1)}, "opts.offset")
	if res := call(t, cur, "graph.unresolved", "kb", map[string]any{"limit": int64(2000)}).(map[string]any); len(res["unresolved"].([]any)) != 5 {
		t.Errorf("limit 2000: %v", res)
	}
	refused(old, map[string]any{"limit": int64(2)}, "protocol 50")
	refused(old, map[string]any{"offset": int64(2)}, "protocol 50")
}

// TestUnresolvedPagesHoldFiveHundredByDefault: with no limit, a page holds 500 rows; a list of 501
// comes in two pages, the second at offset 500 holding the last row.
func TestUnresolvedPagesHoldFiveHundredByDefault(t *testing.T) {
	r := serve(t)
	links := make([]string, 501)
	for i := range links {
		links[i] = fmt.Sprintf("[[gone-%03d]]", i)
	}
	if _, err := r.fsys.WriteFile(context.Background(), "many.md",
		strings.NewReader("# Many\n\n"+strings.Join(links, " ")+"\n")); err != nil {
		t.Fatal(err)
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "many.md's 501 links indexed", func() bool {
		res, err := cur.Call(context.Background(), "graph.unresolved", "kb", map[string]any{"limit": int64(2000)})
		page, _ := res.(map[string]any)
		return err == nil && page["count"] == int64(501)
	})
	first := call(t, cur, "graph.unresolved", "kb").(map[string]any)
	if n := len(first["unresolved"].([]any)); n != 500 || first["count"] != int64(501) || first["next"] != int64(500) {
		t.Fatalf("the first page: %d rows, count %v, next %v; want 500, 501, 500", n, first["count"], first["next"])
	}
	last := call(t, cur, "graph.unresolved", "kb", map[string]any{"offset": int64(500)}).(map[string]any)
	if _, more := last["next"]; more || len(last["unresolved"].([]any)) != 1 {
		t.Errorf("the page at 500: %v, want the last row and no next", last)
	}
}
