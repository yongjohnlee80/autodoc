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

// themeSources are the theme as QML sources, "Theme.<role>", and the roles declared: every role
// of themes.Schema() and every role the host sent, each the host's colour or the dark theme's.
// Sources are fixed before the tree mounts, so a later theme can set only these.
func themeSources(t plugin.Theme) (map[string]any, []string) {
	fallback, _ := themes.Values(fallbackTheme)
	roles := map[string]string{}
	for _, r := range themes.Schema() {
		roles[r.Key] = fallback[r.Key]
	}
	for k, v := range t.Colors {
		roles[k] = v
	}
	out := make(map[string]any, len(roles))
	for k, v := range roles {
		out["Theme."+k] = v
	}
	return out, slices.Sorted(maps.Keys(roles))
}

// themeValues are t's colours for the roles declared at the start; a role it leaves out goes back
// to the dark theme's, and a role not declared then is dropped (it has no source to set).
func themeValues(t plugin.Theme, known []string) map[string]any {
	fallback, _ := themes.Values(fallbackTheme)
	out := make(map[string]any, len(known))
	for _, k := range known {
		v, ok := t.Colors[k]
		if !ok {
			v = fallback[k]
		}
		out["Theme."+k] = v
	}
	return out
}

// colorsOf is t's colours, for the backend to resolve a default colour a swap needs.
func colorsOf(t plugin.Theme) map[string]string { return maps.Clone(t.Colors) }
