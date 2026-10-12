package tui

import (
	"fmt"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// A reset shows exactly its items, each row made from the item and its place; At answers an index
// QML may give, -1 or past the end included, without a check of the caller's own.
func TestRowsKeepTheItemsAndTheModelInStep(t *testing.T) {
	r := newRows(tuidecl.NewListModel("key", "label"), func(i int, s string) rowOf {
		return rowOf{"key": fmt.Sprint(i), "label": s}
	})
	r.Reset([]string{"a", "b"})
	if r.Len() != 2 || r.Model().Len() != 2 || r.Model().At(1)["label"] != "b" || r.Model().At(1)["key"] != "1" {
		t.Fatalf("after a reset: %d items, %d rows, row 1 %v", r.Len(), r.Model().Len(), r.Model().At(1))
	}
	if s, ok := r.At(1); !ok || s != "b" {
		t.Errorf("At(1) = %q, %v", s, ok)
	}
	for _, i := range []int{-1, 2} {
		if s, ok := r.At(i); ok || s != "" {
			t.Errorf("At(%d) = %q, %v; want none", i, s, ok)
		}
	}
	r.Reset(nil)
	if r.Len() != 0 || r.Model().Len() != 0 || len(r.All()) != 0 {
		t.Errorf("after an empty reset: %d items, %d rows", r.Len(), r.Model().Len())
	}
	if _, ok := r.At(0); ok {
		t.Error("At(0) answered after the items went")
	}
}
