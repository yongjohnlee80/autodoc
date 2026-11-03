package tui

import (
	"maps"
	"slices"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// rows is a list the screen shows: its items as Go has them, and the ListModel QML reads, kept in
// step. Reset and Update are the ways the items change, so the model always shows exactly them; At is the
// one bounds check a row index from QML gets.
type rows[T any] struct {
	model *tuidecl.ListModel
	row   func(i int, item T) rowOf
	items []T
}

// newRows is a list shown through model, each item as row makes it from the item and its place.
func newRows[T any](model *tuidecl.ListModel, row func(i int, item T) rowOf) *rows[T] {
	return &rows[T]{model: model, row: row}
}

// Reset replaces the items, and the model's rows with them.
func (r *rows[T]) Reset(items []T) {
	r.items = items
	out := make([]rowOf, len(items))
	for i, it := range items {
		out[i] = r.row(i, it)
	}
	r.model.Reset(out)
}

// Update makes the items items, as Reset does, by the model's own row changes where they say it:
// the old items, each under the key key gives it, kept as a run in the new ones from their first
// (rows above it new, rows below it dropped or new), are changed in place, only the rows that
// differ, and the rest inserted or removed around them. A view on the model then keeps its cursor on
// the row it was on, where a reset moves it to the top (a list's cursor jumping as a message is
// marked read, a page is read under it, or new mail arrives above it). Any other change is a Reset.
// The rows' values compare with ==: strings and numbers, as the lists shown hold.
func (r *rows[T]) Update(items []T, key func(T) string) {
	old := r.items
	off := -1
	if len(old) > 0 {
		first := key(old[0])
		off = slices.IndexFunc(items, func(it T) bool { return key(it) == first })
	}
	kept := 0
	if off >= 0 {
		kept = min(len(old), len(items)-off)
		if !slices.EqualFunc(items[off:off+kept], old[:kept], func(a, b T) bool { return key(a) == key(b) }) ||
			(kept < len(old) && off+kept < len(items)) {
			off = -1
		}
	}
	if off < 0 {
		r.Reset(items)
		return
	}
	r.items = items
	for i := range kept {
		if row := r.row(off+i, items[off+i]); !maps.Equal(row, r.model.At(i)) {
			r.model.Set(i, row)
		}
	}
	if gone := len(old) - kept; gone > 0 {
		r.model.Remove(kept, gone)
	}
	if below := items[off+kept:]; len(below) > 0 {
		r.model.Insert(kept, r.rowsOf(off+kept, below)...)
	}
	if off > 0 {
		r.model.Insert(0, r.rowsOf(0, items[:off])...)
	}
}

// rowsOf is items' rows, the first at place from.
func (r *rows[T]) rowsOf(from int, items []T) []rowOf {
	out := make([]rowOf, len(items))
	for i, it := range items {
		out[i] = r.row(from+i, it)
	}
	return out
}

// At is item i, and whether there is one: an index from QML may be -1, or one a reset since has
// taken away.
func (r *rows[T]) At(i int) (T, bool) {
	if i < 0 || i >= len(r.items) {
		var zero T
		return zero, false
	}
	return r.items[i], true
}

// Len is how many items there are.
func (r *rows[T]) Len() int { return len(r.items) }

// All is the items, in order; the caller does not change them.
func (r *rows[T]) All() []T { return r.items }

// Model is the ListModel QML reads.
func (r *rows[T]) Model() *tuidecl.ListModel { return r.model }
