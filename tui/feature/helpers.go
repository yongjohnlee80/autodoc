package feature

import (
	"fmt"
	"regexp"
	"strconv"

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

// SetField writes a form field bound to name, moving it away first so the same text written twice
// still reaches a field the user has typed in.
func SetField(c Core, name, text string) {
	c.Set(name, "\x00")
	c.Set(name, text)
}

// argRef is a %1 … %99 in a message, which Fill fills.
var argRef = regexp.MustCompile(`%[1-9][0-9]?`)

// Fill is s with each %N its argument, in one pass, so an argument that says "%1" itself (a path, a
// command, a search) is shown as it is. A %N past the arguments stays, but for a two-digit one whose
// first digit names one ("%10" with one argument): the argument, then the digit, as Qt's arg.
func Fill(s string, args ...any) string {
	if len(args) == 0 {
		return s
	}
	return argRef.ReplaceAllStringFunc(s, func(ref string) string {
		if n, _ := strconv.Atoi(ref[1:]); n <= len(args) {
			return fmt.Sprint(args[n-1])
		}
		if n := int(ref[1] - '0'); len(ref) == 3 && n <= len(args) {
			return fmt.Sprint(args[n-1]) + ref[2:]
		}
		return ref
	})
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
