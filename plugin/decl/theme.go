package decl

import (
	"maps"
	"slices"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// The module the Theme singleton is declared in: a layout writes `import autodoc.plugin 1.0` and
// binds its palette to Theme.app.window and the rest.
const (
	themeModule  = "autodoc.plugin"
	themeVersion = "1.0"
)

// fallbackTheme fills a role the host's theme leaves out.
const fallbackTheme = "dark"

// palette is the theme as the card wears it: the dark theme's colours, with the host's over them. A
// role the host leaves out (an empty or partial theme) keeps the dark theme's, for the sources and
// for the colours a reversed or cursor cell resolves alike. Neither map is changed.
func palette(t plugin.Theme) map[string]string {
	fallback, _ := themes.Values(fallbackTheme)
	out := maps.Clone(fallback)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, t.Colors)
	return out
}

// themeSources are the theme as QML sources, "Theme.<role>", and the roles declared: every role of
// themes.Schema() and every role the host sent, each its colour in palette(t). Sources are fixed
// before the tree mounts, so a later theme can set only these.
func themeSources(t plugin.Theme) (map[string]any, []string) {
	p := palette(t)
	roles := map[string]bool{}
	for _, r := range themes.Schema() {
		roles[r.Key] = true
	}
	for k := range t.Colors {
		roles[k] = true
	}
	out := make(map[string]any, len(roles))
	for k := range roles {
		out["Theme."+k] = p[k]
	}
	return out, slices.Sorted(maps.Keys(roles))
}

// themeValues are palette(t) for the roles declared at the start; a role not declared then is
// dropped (it has no source to set).
func themeValues(t plugin.Theme, known []string) map[string]any {
	p := palette(t)
	out := make(map[string]any, len(known))
	for _, k := range known {
		out["Theme."+k] = p[k]
	}
	return out
}

// colorsOf is the card's palette, for the backend to resolve a default colour a swap needs: the
// same colours the Theme.* sources hold.
func colorsOf(t plugin.Theme) map[string]string { return palette(t) }
