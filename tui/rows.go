package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// rows is a list the screen shows: its items as Go has them, and the ListModel QML reads, kept in
// step. Reset is the one way the items change, so the model always shows exactly them; At is the
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
