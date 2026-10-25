package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
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

// builtinThemeRows are Options › Theme's rows for the themes golib ships, the one on screen checked.
func (h *Host) builtinThemeRows() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label", "checked")
	labels := themes.MenuLabels() // golib's order, its five first, and its words with their access keys
	rows := make([]rowOf, 0, len(labels))
	for _, l := range labels {
		var label any = l.Name
		if l.Text != l.Name { // golib has words for it: in the UI's language (language_catalog_test.go holds it)
			label = tuicore.Msg("autodoc.theme." + l.Name)
		}
		rows = append(rows, rowOf{"key": l.Name, "label": label, "checked": l.Name == h.theme})
	}
	m.Reset(rows)
	return m
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
	base, err := h.layoutBaseNow()
	if err != nil {
		h.failed("theme", err)
		return
	}
	if themeOf(base) == "" {
		h.notify(h.tr("autodoc.notice.theme.main_qml_imports_no_theme_to_switch"))
		return
	}
	nextBase := themeImport.ReplaceAll(base, []byte("import autodoc.theme."+name+" "+moduleVersion))
	// after the handler: a menu row's signal is still being emitted, and the engine reconciles only
	// between emissions
	h.p.Post(func() {
		next := h.plugins.withCardPanels(nextBase) // the card set as it is when the reload runs
		if _, err := h.p.Reload(next); err != nil {
			h.failed("theme", err)
			return
		}
		h.layoutSrc, h.layoutBase, h.theme = next, nextBase, name
		h.set("App.userThemes", h.userThemeRows())
		h.set("App.builtinThemes", h.builtinThemeRows())
		h.reg.themeChanged()
		h.say(h.trf("autodoc.notice.theme.theme", name))
	})
}

// layoutBaseNow is the layout before the card panels are written in: layoutBase, or -dev's
// main.qml read again with the GUI's relation views.
func (h *Host) layoutBaseNow() ([]byte, error) {
	if h.dev == "" {
		return h.layoutBase, nil
	}
	b, err := os.ReadFile(path.Join(h.dev, "main.qml"))
	if err != nil {
		return nil, err
	}
	return withRelationViews(b, h.gui), nil
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
	m := tuidecl.NewListModel("key", "label", "enabled", "checked")
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
		rows = append(rows, rowOf{"key": n, "label": label, "enabled": err == nil, "checked": n == h.theme})
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

// reloaded is a hot reload applied: the themes it may have changed, and the keys a key file moved,
// as the cards and help name them.
func (h *Host) reloaded() {
	h.keysChanged()
	h.panelText.apply(h, agentScaleOf(h.prefs)) // the rebuilt agent and terminal panels get their views again
	h.applyTerminalKeymap(h.prefs)              // and their keymap's normal mode
	h.attachTabViewMenus()                      // and the explorer's and the todos' tabs their menu
	h.themesReloaded()
}

// keysChanged is the App's key map installed or replaced: each card and help name the keys as it binds
// them.
func (h *Host) keysChanged() {
	h.keys.live = h.p.App().Keymap()
	for name, text := range h.keys.cardState() {
		h.set(name, text)
	}
	h.applyTerminalKeymap(h.prefs) // the normal-mode hint names the mode key as it is bound now
	h.commandsChanged()
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
