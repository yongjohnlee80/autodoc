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

// themeMenuLabels are Options › Theme's words for golib's themes, each with its access key; a theme
// golib ships that is not here shows as its name, without one.
var themeMenuLabels = map[string]string{
	"dark": "&Dark", "light": "&Light", "mono": "&Mono", "retro": "&Retro", "sepia": "&Sepia",
	"catppuccin_frappe": "&Catppuccin Frappé", "kanagawa": "&Kanagawa", "nord": "&Nord",
	"shades_of_purple": "Shades of &Purple", "tokyo_night": "&Tokyo Night",
}

// themeMenuFirst are the themes Options › Theme lists first, in this order; golib's others follow
// in its order.
var themeMenuFirst = []string{"dark", "light", "mono", "retro", "sepia"}

// builtinThemeRows are Options › Theme's rows for the themes golib ships, the one on screen checked.
func (h *Host) builtinThemeRows() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label", "checked")
	order := slices.DeleteFunc(slices.Clone(themeMenuFirst), func(n string) bool { return !slices.Contains(themeNames, n) })
	for _, n := range themeNames {
		if !slices.Contains(order, n) {
			order = append(order, n)
		}
	}
	rows := make([]rowOf, 0, len(order))
	for _, n := range order {
		var label any = n
		if _, ok := themeMenuLabels[n]; ok {
			label = tuicore.Msg("autodoc.theme." + n) // in the UI's language (language_catalog_test.go holds it)
		}
		rows = append(rows, rowOf{"key": n, "label": label, "checked": n == h.theme})
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
	src := h.layoutSrc
	if h.dev != "" {
		b, err := os.ReadFile(path.Join(h.dev, "main.qml"))
		if err != nil {
			h.failed("theme", err)
			return
		}
		src = h.plugins.withCardPanels(withRelationViews(b, h.gui))
	}
	if themeOf(src) == "" {
		h.notify(h.tr("autodoc.notice.theme.main_qml_imports_no_theme_to_switch"))
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
		h.set("App.userThemes", h.userThemeRows())
		h.set("App.builtinThemes", h.builtinThemeRows())
		h.reg.themeChanged()
		h.say(h.trf("autodoc.notice.theme.theme", name))
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
	h.agentText.apply(h, agentScaleOf(h.prefs)) // the rebuilt agents panel gets its views again
	h.themesReloaded()
}

// keysChanged is the App's key map installed or replaced: each card and help name the keys as it binds
// them.
func (h *Host) keysChanged() {
	h.keys.live = h.p.App().Keymap()
	for name, text := range h.keys.cardState() {
		h.set(name, text)
	}
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
