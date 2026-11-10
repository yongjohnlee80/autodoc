//go:build gui

package relations

import (
	"cmp"
	"image/color"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// THE GLOBEMAP'S SETTINGS — Relations › GlobeMap settings… and Preferences › GlobeMap…: the
// GlobeMap's own preferences, kept in the store as tui.globemap.*. Its theme (the terminals', by
// default; the page's; or one named), whether it names documents, the links it follows when it
// opens, its dots' size, how fast it settles, and colours of the user's own over the theme's: a
// document no group matches, each link class, and the superseded ring. A colour field takes
// #rrggbb, or nothing for the theme's. Its groups, which colour the documents, are the workspace's
// graph file's (groups.go, graphconf.go), edited in the same dialog (groupeditor_gui.go).

const (
	prefGlobeTheme  = "tui.globemap.theme"
	prefGlobeDots   = "tui.globemap.dots"
	prefGlobeSpeed  = "tui.globemap.speed"
	PrefGlobeColour = "tui.globemap.colour." // and a globeColourFields key
)

// globeDotSizes are the dots' sizes, by their stored name.
var globeDotSizes = []struct {
	name  string
	scale float32
}{{"small", 0.7}, {"medium", 1}, {"large", 1.4}}

// globeSpeeds are how fast the map settles: the simulation's steps between pictures shown.
var globeSpeeds = []struct {
	name, label string
	steps       int
}{{"slow", "slow", 1}, {"gentle", "gentle", 2}, {"normal", "normal", 4}, {"fast", "fast", 8}}

// globeColourFields are the colours the user may give, each a text field and a preference: a
// document no group matches, per link class (edgeClass), and the ring round a superseded document.
var globeColourFields = []struct{ key, source string }{
	{"other", "App.globeOtherColour"},
	{"link.body", "App.globeBodyLinkColour"}, {"link.supersession", "App.globeSupersessionLinkColour"},
	{"link.relation", "App.globeRelationLinkColour"}, {"superseded", "App.globeRingColour"},
}

// globeLook is how the GlobeMap draws: its theme's colours with the user's over them, its names,
// its dots' scale and its pace. Made on the loop, never changed once made: a load's pictures read it.
type globeLook struct {
	pal   relPalette
	other color.NRGBA // a document no group matches
	edges [edgeClasses]color.NRGBA
	ring  color.NRGBA
	names bool
	dot   float32 // the dots' scale
	steps int     // simulation steps between pictures shown
}

// defaultLook is the look of no preferences in the default palette: a test's view.
var defaultLook = newGlobeLook(unsetPrefs{}, defaultPalette())

// unsetPrefs is preferences with nothing set: every look's default.
type unsetPrefs struct{}

func (unsetPrefs) Value(string) string                     { return "" }
func (unsetPrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }

// newGlobeLook is the look of preferences p over the theme's palette pal. A colour or a choice
// that does not read is the default's.
func newGlobeLook(p feature.PrefsView, pal relPalette) *globeLook {
	l := &globeLook{pal: pal, names: p.Value(PrefGlobeNames) != "false", dot: 1, steps: 2}
	for _, d := range globeDotSizes {
		if d.name == p.Value(prefGlobeDots) {
			l.dot = d.scale
		}
	}
	for _, s := range globeSpeeds {
		if s.name == p.Value(prefGlobeSpeed) {
			l.steps = s.steps
		}
	}
	colour := func(key string, def color.NRGBA) color.NRGBA {
		if c, ok := parseHex(p.Value(PrefGlobeColour + key)); ok {
			return c
		}
		return def
	}
	l.other = colour("other", pal.text)
	l.edges = [edgeClasses]color.NRGBA{
		edgeBody:         colour("link.body", mixNRGBA(pal.background, pal.muted, 0.8)),
		edgeSupersession: colour("link.supersession", mixNRGBA(pal.background, pal.superseded, 0.7)),
		edgeRelation:     colour("link.relation", mixNRGBA(pal.background, pal.relation, 0.7)),
	}
	l.ring = colour("superseded", pal.superseded)
	return l
}

// colourOf is document n's dot's colour: its group's (the first that matches), else other's.
func (l *globeLook) colourOf(n mapNode, gs *relGroups) color.NRGBA {
	if gs != nil {
		if i := gs.of(n); i >= 0 {
			return gs.list[i].Colour
		}
	}
	return l.other
}

// globeThemeSource is the host's themes, as the GlobeMap reads them (core's).
type globeThemeSource interface {
	Theme() string // the page's
	ThemeValues(name string) (map[string]string, error)
	ThemeNames() []string
	ThemeLabels() []string
}

// globeThemeValues are the values of the theme the GlobeMap is drawn in for p: the terminals' by
// default (viewThemeValues).
func globeThemeValues(p feature.PrefsView, ts globeThemeSource) (map[string]string, bool) {
	return viewThemeValues(cmp.Or(p.Value(prefGlobeTheme), "terminal"), p, ts)
}

// viewThemeValues are the values of the theme a view chose: the terminals' (feature.TerminalThemeValues),
// the page's, or one named; one that can't be read is the page's.
func viewThemeValues(choice string, p feature.PrefsView, ts globeThemeSource) (map[string]string, bool) {
	page := ts.Theme()
	name := choice
	switch name {
	case "terminal":
		return feature.TerminalThemeValues(p, page, ts.ThemeValues)
	case "", "page":
		name = page
	}
	if v, err := ts.ThemeValues(name); err == nil {
		return v, true
	}
	v, err := ts.ThemeValues(page)
	return v, err == nil
}

// themeChoices are the theme chooser's values, and its labels.
func (g *GlobeMap) themeChoices() (names, labels []string) { return viewThemeChoices(g.c) }

// viewThemeChoices are a view's theme chooser's values and labels: the terminals', the page's (in
// the UI's language), then every theme the window can wear.
func viewThemeChoices(c feature.Core) (names, labels []string) {
	return feature.ThemeChoices(c, []string{"terminal", "page"}, []string{
		c.Tr("autodoc.text.globesettings_gui.the_terminals"), c.Tr("autodoc.text.globesettings_gui.the_page_s")})
}

// lookNow is the look the preferences and the themes give now.
func (g *GlobeMap) lookNow() *globeLook {
	p, pal := g.c.Prefs(), defaultPalette()
	if ts, ok := g.c.(globeThemeSource); ok {
		if v, ok := globeThemeValues(p, ts); ok {
			pal = paletteOf(v, pal)
		}
	}
	return newGlobeLook(p, pal)
}

// currentLook is the look in effect, made at its first use.
func (g *GlobeMap) currentLook() *globeLook {
	if g.look == nil {
		g.look = g.lookNow()
	}
	return g.look
}

// restyle takes the look again: when it changed, the load under way draws its frame in it.
func (g *GlobeMap) restyle() {
	l := g.lookNow()
	if g.look != nil && reflect.DeepEqual(*g.look, *l) {
		return
	}
	g.look = l
	if g.view != nil && g.view.ctx != nil {
		g.SendView(g.view.pixels(), false)
		g.dirty()
	}
}

// ThemeChanged: the page's theme, which the GlobeMap's may follow.
func (g *GlobeMap) ThemeChanged() { g.restyle() }

// PreferencesChanged: a choice in a dialog (this one's choosers, the terminals' theme).
func (g *GlobeMap) PreferencesChanged() { g.restyle() }

// PrefSet: a preference set alone — the GlobeMap's own fields, or the terminals' theme.
func (g *GlobeMap) PrefSet(name string) {
	if name == feature.PrefTermTheme || strings.HasPrefix(name, "tui.globemap.") {
		g.restyle()
	}
}

func (g *GlobeMap) Prefs() []feature.PrefSpec {
	names, _ := g.themeChoices()
	keys := func(n int, key func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = key(i)
		}
		return out
	}
	text := func(source, name string) func(feature.PrefsView) map[string]any {
		return func(p feature.PrefsView) map[string]any { return map[string]any{source: p.Value(name)} }
	}
	rows := []feature.PrefSpec{
		feature.ValuePref(prefGlobeTheme, "terminal", nil).Shows(func(p feature.PrefsView) map[string]any {
			return map[string]any{"App.globeThemeIndex": max(slices.Index(names, p.Value(prefGlobeTheme)), 0)}
		}),
		feature.ValuePref(PrefGlobeNames, "true", []string{"true", "false"}).Chooser("App.globeNamesIndex", "App.setGlobeNames"),
		feature.ValuePref(PrefGlobeFilter, globeFilters[0].key, globeFilterKeys()).
			Chooser("App.globeFilterIndex", "App.setGlobeFilter").Then(func(feature.PrefsView) { g.filterChosen() }),
		feature.ValuePref(prefGlobeDots, "medium", keys(len(globeDotSizes), func(i int) string { return globeDotSizes[i].name })).
			Chooser("App.globeDotsIndex", "App.setGlobeDots"),
		feature.ValuePref(prefGlobeSpeed, "gentle", keys(len(globeSpeeds), func(i int) string { return globeSpeeds[i].name })).
			Chooser("App.globeSpeedIndex", "App.setGlobeSpeed"),
	}
	for _, f := range globeColourFields {
		rows = append(rows, feature.ValuePref(PrefGlobeColour+f.key, "", nil).Shows(text(f.source, PrefGlobeColour+f.key)))
	}
	return rows
}

// prefFilter is the filter the preferences open the map with.
func (g *GlobeMap) prefFilter() int { return globeFilterPref(g.c) }

// filterChosen: the map shown follows the links chosen.
func (g *GlobeMap) filterChosen() {
	if f := g.prefFilter(); g.open && f != g.filter {
		g.filter = f
		g.Load()
	}
}

// openSettings is Relations › GlobeMap settings… and Preferences › GlobeMap….
func (g *GlobeMap) OpenSettings() {
	g.c.Set("App.globeSettingsError", "")
	g.c.Open("globeMapSettings")
	if ws := g.c.Workspace(); ws != "" && (g.editor.loading || g.editor.ws != ws) {
		var types []string
		if g.cur != nil && g.cur.ws == ws {
			types = g.cur.m.Types
		}
		g.editor.loadingFor(ws)
		g.readGroups(ws, types) // its groups, read now: the map may not have been opened since ws was entered
	}
}

// setTheme is the theme chooser's row i.
func (g *GlobeMap) setTheme(i int) {
	if names, _ := g.themeChoices(); i >= 0 && i < len(names) {
		g.c.SetPref(prefGlobeTheme, names[i])
	}
}

// setColour is a colour field, as it is typed: #rrggbb, or nothing for the theme's, applies at once;
// another says what the field takes, and changes nothing.
func (g *GlobeMap) SetColour(key, text string) {
	if !slices.ContainsFunc(globeColourFields, func(f struct{ key, source string }) bool { return f.key == key }) {
		return
	}
	text = strings.TrimSpace(text)
	if _, ok := parseHex(text); text != "" && !ok {
		g.c.Set("App.globeSettingsError", g.c.Tr("autodoc.text.globesettings_gui.a_colour_is_rrggbb_or_nothing_for_the_theme_s"))
		return
	}
	g.c.Set("App.globeSettingsError", "")
	g.c.SetPref(PrefGlobeColour+key, text)
}

// settingsState is the dialog's lists.
func (g *GlobeMap) settingsState() map[string]any {
	names, themes := g.themeChoices()
	st := g.editor.state()
	maps.Copy(st, map[string]any{
		"App.globeThemes": feature.KeyedChoices(names, themes), "App.globeFilters": relationChoice(g.c, "App.globeFilters"),
		"App.globeDotSizes": relationChoice(g.c, "App.globeDotSizes"), "App.globeSpeeds": relationChoice(g.c, "App.globeSpeeds"),
		"App.globeSettingsError": "",
	})
	return st
}

func (g *GlobeMap) settingsCommands() map[string]feature.Command {
	c := g.editor.commands()
	c["App.setGlobeTheme"] = feature.OneNumber("a row", g.setTheme)
	c["App.setGlobeColour"] = feature.TwoStrings("a colour's name and #rrggbb", g.SetColour)
	return c
}
