package tui

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// SWITCHING THEME — View › Theme.
//
// A theme is chosen by one line of main.qml, its import (`import autodoc.theme.dark 1.0`), so
// switching theme at runtime is that line, rewritten, and the layout reloaded: the path a hot
// reload takes. The file, the cursor and the panes stay as they were. Under -dev the file on disk
// stays the authority, and its next save brings back its own import.

var themeImport = regexp.MustCompile(`(?m)^import autodoc\.theme\.([a-z][a-z0-9_]*) ` + regexp.QuoteMeta(moduleVersion))

// themeOf is the theme a layout imports, "" for none.
func themeOf(src []byte) string {
	if m := themeImport.FindSubmatch(src); m != nil {
		return string(m[1])
	}
	return ""
}

// themeState is which theme the menu shows checked: the one the layout imports.
func themeState(theme string) map[string]any {
	return map[string]any{"App.activeTheme": theme, "App.themeDark": theme == "dark", "App.themeLight": theme == "light",
		"App.themeMono": theme == "mono", "App.themeRetro": theme == "retro", "App.themeSepia": theme == "sepia"}
}

// themeNames are the themes golib ships, as the menu and the Preferences dialog offer them.
var themeNames = themes.Names()

// isTheme reports a theme golib ships.
func isTheme(name string) bool {
	_, err := fs.Stat(themes.FS(), path.Join(".", name+".qml"))
	return err == nil
}

// switchTheme puts the named theme on screen (useTheme also keeps it); one golib does not ship is
// refused.
func (h *Host) switchTheme(name string) {
	if _, err := h.themeValues(name); err != nil {
		h.notify(err.Error())
		name = h.defaultTheme()
	}
	src := h.layoutSrc
	if h.dev != "" {
		b, err := os.ReadFile(path.Join(h.dev, "main.qml"))
		if err != nil {
			h.failed("theme", err)
			return
		}
		src = b
	}
	if themeOf(src) == "" {
		h.notify("main.qml imports no theme to switch")
		return
	}
	next := themeImport.ReplaceAll(src, []byte("import autodoc.theme."+name+" "+moduleVersion))
	// after the handler: a menu row's signal is still being emitted, and the engine reconciles only
	// between emissions
	h.p.Post(func() {
		if _, err := h.p.Reload(next); err != nil {
			h.failed("theme", err)
			return
		}
		h.layoutSrc, h.theme = next, name
		h.reg.themeChanged()
		for k, v := range themeState(name) {
			h.set(k, v)
		}
		h.say("theme: " + name)
	})
}

func (h *Host) defaultTheme() string {
	if h.gui {
		return defaultGUITheme
	}
	return defaultTheme
}
func (h *Host) themeNames() []string {
	if h.themeCatalog == nil {
		return themes.Names()
	}
	return h.themeCatalog.Names()
}
func (h *Host) themeLabels() []string {
	if h.themeCatalog == nil {
		return themes.Names()
	}
	return h.themeCatalog.Labels()
}
func (h *Host) themeValues(name string) (map[string]string, error) {
	if h.themeCatalog == nil {
		return themes.Values(name)
	}
	return h.themeCatalog.Values(name)
}
func (h *Host) userThemeRows() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label", "enabled")
	var rows []rowOf
	for _, n := range h.themeNames() {
		if slices.Contains(themeNames, n) {
			continue
		}
		_, err := h.themeValues(n)
		label := n + " (user)"
		if err != nil {
			label += " — invalid"
		}
		rows = append(rows, rowOf{"key": n, "label": label, "enabled": err == nil})
	}
	m.Reset(rows)
	return m
}

func (h *Host) themeReloadError(err error) {
	h.notify(err.Error())
	h.set("App.userThemes", h.userThemeRows())
	if !isTheme(h.theme) {
		if _, bad := h.themeValues(h.theme); bad != nil {
			h.switchTheme(h.defaultTheme())
		}
	}
}

func (h *Host) themesReloaded() {
	h.set("App.userThemes", h.userThemeRows())
	h.set("App.themes", choices(h.themeLabels()...))
	chosen := h.prefs.theme
	if chosen != "" && !isTheme(chosen) {
		if _, err := h.themeValues(chosen); err == nil && chosen != h.theme {
			h.switchTheme(chosen)
			return
		}
	}
	h.reg.themeChanged()
}
