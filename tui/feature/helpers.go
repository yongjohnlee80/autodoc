package feature

import (
	"fmt"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// Choices are a chooser's rows: each a label.
func Choices(labels ...string) *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	rows := make([]RowOf, len(labels))
	for i, l := range labels {
		rows[i] = RowOf{"key": l, "label": l}
	}
	m.Reset(rows)
	return m
}

// Wording is a catalog's text for a function that composes it: a feature's Core in the UI's
// language, or the host's English (tui's inEnglish).
type Wording interface {
	Tr(id string) string
	Trf(id string, args ...any) string
}

// BytesText is a size as the list shows it.
func BytesText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
