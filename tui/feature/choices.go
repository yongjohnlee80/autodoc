package feature

import (
	"fmt"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// PrefTermTheme is the preference naming the theme the terminals are drawn in: "page" (or unset)
// draws them in the page's. The relation views can be drawn in it too.
const PrefTermTheme = "tui.terminal.theme"

// KeyedChoices is a chooser's rows: row i keyed keys[i] (a value, never translated), labelled
// labels[i] (in the UI's language).
func KeyedChoices(keys, labels []string) *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	rows := make([]RowOf, len(keys))
	for i, k := range keys {
		rows[i] = RowOf{"key": k, "label": labels[i]}
	}
	m.Reset(rows)
	return m
}

// ThemeChoices are a theme chooser's values and labels: lead's first (the page's, the terminals'),
// then every theme the window can wear (c's ThemeNames and ThemeLabels), else golib's.
func ThemeChoices(c any, lead, leadLabels []string) (names, labels []string) {
	names, labels = slices.Clone(lead), slices.Clone(leadLabels)
	n, ok1 := c.(interface{ ThemeNames() []string })
	l, ok2 := c.(interface{ ThemeLabels() []string })
	if ok1 && ok2 {
		return append(names, n.ThemeNames()...), append(labels, l.ThemeLabels()...)
	}
	shipped := themes.Names()
	return append(names, shipped...), append(labels, shipped...)
}

// TermThemeOf is the terminals' theme as p holds it; "" with no preferences (a cell's own terminals).
func TermThemeOf(p PrefsView) string {
	if p == nil {
		return ""
	}
	return p.Value(PrefTermTheme)
}

// TerminalThemeValues are the values of the theme the terminals are drawn in, for preferences p on
// a page drawn in page: the one chosen, else the page's; one that can't be read falls back to the
// page's. values reads a theme by name (the host's: golib's or the user's). false when neither reads.
func TerminalThemeValues(p PrefsView, page string, values func(string) (map[string]string, error)) (map[string]string, bool) {
	name := TermThemeOf(p)
	if name == "" || name == "page" {
		name = page
	}
	if v, err := values(name); err == nil {
		return v, true
	}
	if v, err := values(page); err == nil {
		return v, true
	}
	return nil, false
}

// Bar is done of total as ten cells, then the numbers.
func Bar(done, total int64) string {
	const width = 10
	done = min(max(done, 0), total)
	filled := int(done * width / max(total, 1))
	return fmt.Sprintf("%s%s %d/%d", strings.Repeat("█", filled), strings.Repeat("░", width-filled), done, total)
}
