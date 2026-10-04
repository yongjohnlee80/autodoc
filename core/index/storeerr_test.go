package index

import (
	"context"
	"reflect"
	"testing"
)

// TestTheStoresReadsFailAsThemselves: a read that fails comes back as an error, from the index's own
// calls: the active model's read for a query, the section size for the rebuild check, and a
// workspace's change head.
func TestTheStoresReadsFailAsThemselves(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, Options{Provider: newFake("m", "a")})
	e.put("a.md", "zebra\n")
	e.ready()
	// a workspace that is not there has no change head
	if _, err := Open(e.db, e.ws+1000).Status(ctx); err == nil {
		t.Error("the status of a missing workspace read a change head")
	}
	sem := e.ix.sem
	e.stop() // the store is closed
	if _, _, err := e.store.embedQuery(ctx, sem, "zebra"); err == nil {
		t.Error("a query embedded with the store closed")
	}
	if _, err := e.store.outdated(ctx, nil); err == nil {
		t.Error("the rebuild check read the section size with the store closed")
	}
}

// TestListPagesInPathOrder: the documents in path order, a page at a time, saying whether more
// follow; a limit of 0 is the most a page holds.
func TestListPagesInPathOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, Options{})
	e.put("c.md", "c\n", "a.md", "a\n", "b.md", "b\n")
	page, more, err := e.store.List(ctx, "", 2)
	if err != nil || !more || len(page) != 2 || page[0].Path != "a.md" || page[1].Path != "b.md" || page[0].Generation == 0 || page[0].Version == "" {
		t.Fatalf("the first page: %+v, more %v, %v", page, more, err)
	}
	rest, more, err := e.store.List(ctx, "b.md", 0)
	if err != nil || more || !reflect.DeepEqual([]string{rest[0].Path}, []string{"c.md"}) || len(rest) != 1 {
		t.Fatalf("the rest: %+v, more %v, %v", rest, more, err)
	}
}
