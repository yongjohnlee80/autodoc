package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// SWITCHING THEME — View › Theme.
//
// A theme is chosen by one line of main.qml, its import (`import autodoc.theme.dark 1.0`), so
// switching theme at runtime is that line, rewritten, and the layout reloaded: the path a hot
// reload takes. The note, the cursor and the panes stay as they were. Under -dev the file on disk
// stays the authority, and its next save brings back its own import.

var themeImport = regexp.MustCompile(`(?m)^import autodoc\.theme\.([a-z][a-z0-9]*) ` + regexp.QuoteMeta(moduleVersion))

// themeOf is the theme a layout imports, "" for none.
func themeOf(src []byte) string {
	if m := themeImport.FindSubmatch(src); m != nil {
		return string(m[1])
	}
	return ""
}

// themeState is which theme the menu shows checked: the one the layout imports.
func themeState(theme string) map[string]any {
	return map[string]any{"App.themeDark": theme == "dark", "App.themeLight": theme == "light",
		"App.themeMono": theme == "mono", "App.themeRetro": theme == "retro"}
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
	if !isTheme(name) {
		h.setStatus(fmt.Sprintf("no theme %q", name))
		return
	}
	src := h.layoutSrc
	if h.dev != "" {
		b, err := os.ReadFile(path.Join(h.dev, "main.qml"))
		if err != nil {
			h.setStatus("theme: " + err.Error())
			return
		}
		src = b
	}
	if themeOf(src) == "" {
		h.setStatus("main.qml imports no theme to switch")
		return
	}
	next := themeImport.ReplaceAll(src, []byte("import autodoc.theme."+name+" "+moduleVersion))
	// after the handler: a menu row's signal is still being emitted, and the engine reconciles only
	// between emissions
	h.p.Post(func() {
		if _, err := h.p.Reload(next); err != nil {
			h.setStatus("theme: " + err.Error())
			return
		}
		h.layoutSrc, h.theme = next, name
		for k, v := range themeState(name) {
			h.set(k, v)
		}
		h.setStatus("theme: " + name)
	})
}
