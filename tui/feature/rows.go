package feature

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// Rows is a list the screen shows: its items as Go has them, and the ListModel QML reads, kept in
// step. Reset is the one way the items change, so the model always shows exactly them; At is the
// one bounds check a row index from QML gets.
type Rows[T any] struct {
	model *tuidecl.ListModel
	Row   func(i int, item T) RowOf
	Items []T
}

// newRows is a list shown through model, each item as row makes it from the item and its place.
func NewRows[T any](model *tuidecl.ListModel, row func(i int, item T) RowOf) *Rows[T] {
	return &Rows[T]{model: model, Row: row}
}

// Reset replaces the items, and the model's rows with them.
func (r *Rows[T]) Reset(items []T) {
	r.Items = items
	out := make([]RowOf, len(items))
	for i, it := range items {
		out[i] = r.Row(i, it)
	}
	r.model.Reset(out)
}

// At is item i, and whether there is one: an index from QML may be -1, or one a reset since has
// taken away.
func (r *Rows[T]) At(i int) (T, bool) {
	if i < 0 || i >= len(r.Items) {
		var zero T
		return zero, false
	}
	return r.Items[i], true
}

// Len is how many items there are.
func (r *Rows[T]) Len() int { return len(r.Items) }

// All is the items, in order; the caller does not change them.
func (r *Rows[T]) All() []T { return r.Items }

// Model is the ListModel QML reads.
func (r *Rows[T]) Model() *tuidecl.ListModel { return r.model }
