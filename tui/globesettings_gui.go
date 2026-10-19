//go:build gui

package tui

import (
	"fmt"
	"image/color"
	"reflect"
	"slices"
	"strings"
)

// THE GLOBEMAP'S SETTINGS — Relations › GlobeMap settings… and Preferences › GlobeMap…: the
// GlobeMap's own preferences, kept in the store as tui.globemap.*. Its theme (the terminals', by
// default; the page's; or one named), whether it names documents, the links it follows when it
// opens, its dots' size, how fast it settles, and colours of the user's own over the theme's: per
// document type, per link class, the superseded ring, and rules by tag or folder, the first that
// matches winning. A colour field takes #rrggbb, or nothing for the theme's.

const (
	prefGlobeTheme  = "tui.globemap.theme"
	prefGlobeNames  = "tui.globemap.names"
	prefGlobeFilter = "tui.globemap.filter"
	prefGlobeDots   = "tui.globemap.dots"
	prefGlobeSpeed  = "tui.globemap.speed"
	prefGlobeRules  = "tui.globemap.rules"
	prefGlobeColour = "tui.globemap.colour." // and a globeColourFields key
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

// globeColourFields are the colours the user may give, each a text field and a preference: per
// document type (relationTypes, then any other), per link class (edgeClass), and the ring round a
// superseded document.
var globeColourFields = []struct{ key, source string }{
	{"adr", "App.globeAdrColour"}, {"review", "App.globeReviewColour"},
	{"convention", "App.globeConventionColour"}, {"synthesis", "App.globeSynthesisColour"},
	{"pr", "App.globePrColour"}, {"other", "App.globeOtherColour"},
	{"link.body", "App.globeBodyLinkColour"}, {"link.supersession", "App.globeSupersessionLinkColour"},
	{"link.relation", "App.globeRelationLinkColour"}, {"superseded", "App.globeRingColour"},
}

// globeLook is how the GlobeMap draws: its theme's colours with the user's over them, its names,
// its dots' scale and its pace. Made on the loop, never changed once made: a load's pictures read it.
type globeLook struct {
	pal   relPalette
	types map[string]color.NRGBA // a document's colour by its frontmatter type
	other color.NRGBA            // a type of no colour of its own
	edges [edgeClasses]color.NRGBA
	ring  color.NRGBA
	rules []globeRule
	names bool
	dot   float32 // the dots' scale
	steps int     // simulation steps between pictures shown
}

// globeRule colours the documents of a tag, or under a folder.
type globeRule struct {
	tag    string // a tag, lower case, without its #; "" for a folder's rule
	folder string // a path prefix, ending in /
	c      color.NRGBA
}

// defaultLook is the look of no preferences in the default palette: a test's view.
var defaultLook = newGlobeLook(prefs{}, defaultPalette())

// newGlobeLook is the look of preferences p over the theme's palette pal. A colour, a rule or a
// choice that does not read is the default's.
func newGlobeLook(p prefs, pal relPalette) *globeLook {
	l := &globeLook{pal: pal, names: p.Value(prefGlobeNames) != "false", dot: 1, steps: 2,
		types: map[string]color.NRGBA{}}
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
		if c, ok := parseHex(p.Value(prefGlobeColour + key)); ok {
			return c
		}
		return def
	}
	for _, t := range relationTypes {
		l.types[t] = colour(t, pal.types[t])
	}
	l.other = colour("other", pal.text)
	l.edges = [edgeClasses]color.NRGBA{
		edgeBody:         colour("link.body", mixNRGBA(pal.background, pal.muted, 0.8)),
		edgeSupersession: colour("link.supersession", mixNRGBA(pal.background, pal.superseded, 0.7)),
		edgeRelation:     colour("link.relation", mixNRGBA(pal.background, pal.relation, 0.7)),
	}
	l.ring = colour("superseded", pal.superseded)
	l.rules, _ = parseGlobeRules(p.Value(prefGlobeRules))
	return l
}

// colourOf is document n's dot's colour: the first rule it matches, else its type's.
func (l *globeLook) colourOf(n mapNode) color.NRGBA {
	for _, r := range l.rules {
		if r.folder != "" && strings.HasPrefix(n.Path, r.folder) {
			return r.c
		}
		if r.tag != "" && slices.ContainsFunc(n.Tags, func(t string) bool {
			t = strings.ToLower(strings.TrimPrefix(t, "#"))
			return t == r.tag || strings.HasPrefix(t, r.tag+"/") // a nested tag is its parent's too
		}) {
			return r.c
		}
	}
	if c, ok := l.types[n.Type]; ok {
		return c
	}
	return l.other
}

// parseGlobeRules reads the rules field: "#tag=#rrggbb; folder/=#rrggbb", rules apart by ; or a
// new line.
func parseGlobeRules(s string) ([]globeRule, error) {
	var out []globeRule
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		c, isHex := parseHex(strings.TrimSpace(v))
		name := strings.Trim(strings.TrimPrefix(k, "#"), "/")
		if !ok || !isHex || name == "" {
			return nil, fmt.Errorf("%q: a rule is #tag=#rrggbb or folder/=#rrggbb", part)
		}
		if strings.HasPrefix(k, "#") {
			out = append(out, globeRule{tag: strings.ToLower(name), c: c})
		} else {
			out = append(out, globeRule{folder: name + "/", c: c})
		}
	}
	return out, nil
}

// globeThemeSource is the host's themes, as the GlobeMap reads them (core's).
type globeThemeSource interface {
	Theme() string // the page's
	ThemeValues(name string) (map[string]string, error)
	ThemeNames() []string
	ThemeLabels() []string
}

// globeThemeValues are the values of the theme the GlobeMap is drawn in for p: the terminals'
// (terminalThemeValues), the page's, or one named; one that can't be read is the page's.
func globeThemeValues(p prefs, ts globeThemeSource) (map[string]string, bool) {
	page := ts.Theme()
	name := p.Value(prefGlobeTheme)
	switch name {
	case "", "terminal":
		return terminalThemeValues(p, page, ts.ThemeValues)
	case "page":
		name = page
	}
	if v, err := ts.ThemeValues(name); err == nil {
		return v, true
	}
	v, err := ts.ThemeValues(page)
	return v, err == nil
}

// themeChoices are the theme chooser's values, and its labels.
func (g *globeMap) themeChoices() (names, labels []string) {
	names, labels = []string{"terminal", "page"}, []string{"the terminals'", "the page's"}
	if ts, ok := g.c.(globeThemeSource); ok {
		return append(names, ts.ThemeNames()...), append(labels, ts.ThemeLabels()...)
	}
	return names, labels
}

// lookNow is the look the preferences and the themes give now.
func (g *globeMap) lookNow() *globeLook {
	p, pal := g.c.Prefs(), defaultPalette()
	if ts, ok := g.c.(globeThemeSource); ok {
		if v, ok := globeThemeValues(p, ts); ok {
			pal = paletteOf(v, pal)
		}
	}
	return newGlobeLook(p, pal)
}

// currentLook is the look in effect, made at its first use.
func (g *globeMap) currentLook() *globeLook {
	if g.look == nil {
		g.look = g.lookNow()
	}
	return g.look
}

// restyle takes the look again: when it changed, the load under way draws its frame in it.
func (g *globeMap) restyle() {
	l := g.lookNow()
	if g.look != nil && reflect.DeepEqual(*g.look, *l) {
		return
	}
	g.look = l
	if g.view != nil && g.view.ctx != nil {
		g.sendView(g.view.pixels(), false)
		g.dirty()
	}
}

// ThemeChanged: the page's theme, which the GlobeMap's may follow.
func (g *globeMap) ThemeChanged() { g.restyle() }

// PreferencesChanged: a choice in a dialog (this one's choosers, the terminals' theme).
func (g *globeMap) PreferencesChanged() { g.restyle() }

// PrefSet: a preference set alone — the GlobeMap's own fields, or the terminals' theme.
func (g *globeMap) PrefSet(name string) {
	if name == prefTermTheme || strings.HasPrefix(name, "tui.globemap.") {
		g.restyle()
	}
}

func (g *globeMap) Prefs() []PrefSpec {
	names, _ := g.themeChoices()
	keys := func(n int, key func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = key(i)
		}
		return out
	}
	text := func(source, name string) func(prefs) map[string]any {
		return func(p prefs) map[string]any { return map[string]any{source: p.Value(name)} }
	}
	rows := []PrefSpec{
		ValuePref(prefGlobeTheme, "terminal", nil).shows(func(p prefs) map[string]any {
			return map[string]any{"App.globeThemeIndex": max(slices.Index(names, p.Value(prefGlobeTheme)), 0)}
		}),
		ValuePref(prefGlobeNames, "true", []string{"true", "false"}).chooser("App.globeNamesIndex", "App.setGlobeNames"),
		ValuePref(prefGlobeFilter, globeFilters[0].key, keys(len(globeFilters), func(i int) string { return globeFilters[i].key })).
			chooser("App.globeFilterIndex", "App.setGlobeFilter").then(func(prefs) { g.filterChosen() }),
		ValuePref(prefGlobeDots, "medium", keys(len(globeDotSizes), func(i int) string { return globeDotSizes[i].name })).
			chooser("App.globeDotsIndex", "App.setGlobeDots"),
		ValuePref(prefGlobeSpeed, "gentle", keys(len(globeSpeeds), func(i int) string { return globeSpeeds[i].name })).
			chooser("App.globeSpeedIndex", "App.setGlobeSpeed"),
		ValuePref(prefGlobeRules, "", nil).shows(text("App.globeRulesText", prefGlobeRules)),
	}
	for _, f := range globeColourFields {
		rows = append(rows, ValuePref(prefGlobeColour+f.key, "", nil).shows(text(f.source, prefGlobeColour+f.key)))
	}
	return rows
}

// prefFilter is the filter the preferences open the map with.
func (g *globeMap) prefFilter() int {
	return max(slices.IndexFunc(globeFilters, func(f globeFilter) bool {
		return f.key == g.c.Prefs().Value(prefGlobeFilter)
	}), 0)
}

// filterChosen: the map shown follows the links chosen.
func (g *globeMap) filterChosen() {
	if f := g.prefFilter(); g.open && f != g.filter {
		g.filter = f
		g.load()
	}
}

// openGlobeSettings is App.openGlobeSettings, the host's in every build (Preferences.qml names it):
// the GlobeMap's settings in a window, and a word of why not elsewhere.
func (h *Host) openGlobeSettings() {
	for _, f := range h.relationViewFeatures() {
		if g, ok := f.(*globeMap); ok {
			g.openSettings()
			return
		}
	}
	h.notify(globeSettingsElsewhere)
}

// openSettings is Relations › GlobeMap settings… and Preferences › GlobeMap….
func (g *globeMap) openSettings() {
	g.c.Set("App.globeSettingsError", "")
	g.c.Open("globeMapSettings")
}

// setTheme is the theme chooser's row i.
func (g *globeMap) setTheme(i int) {
	if names, _ := g.themeChoices(); i >= 0 && i < len(names) {
		g.c.SetPref(prefGlobeTheme, names[i])
	}
}

// setColour is a colour field, as it is typed: #rrggbb, or nothing for the theme's, applies at once;
// another says what the field takes, and changes nothing.
func (g *globeMap) setColour(key, text string) {
	if !slices.ContainsFunc(globeColourFields, func(f struct{ key, source string }) bool { return f.key == key }) {
		return
	}
	text = strings.TrimSpace(text)
	if _, ok := parseHex(text); text != "" && !ok {
		g.c.Set("App.globeSettingsError", "a colour is #rrggbb, or nothing for the theme's")
		return
	}
	g.c.Set("App.globeSettingsError", "")
	g.c.SetPref(prefGlobeColour+key, text)
}

// setRules is the rules field, as it is typed: rules that read apply at once.
func (g *globeMap) setRules(text string) {
	if _, err := parseGlobeRules(text); err != nil {
		g.c.Set("App.globeSettingsError", err.Error())
		return
	}
	g.c.Set("App.globeSettingsError", "")
	g.c.SetPref(prefGlobeRules, strings.TrimSpace(text))
}

// settingsState is the dialog's lists.
func (g *globeMap) settingsState() map[string]any {
	_, themes := g.themeChoices()
	var filters, dots, speeds []string
	for _, f := range globeFilters {
		filters = append(filters, f.name)
	}
	for _, d := range globeDotSizes {
		dots = append(dots, d.name)
	}
	for _, s := range globeSpeeds {
		speeds = append(speeds, s.label)
	}
	return map[string]any{
		"App.globeThemes": choices(themes...), "App.globeFilters": choices(filters...),
		"App.globeDotSizes": choices(dots...), "App.globeSpeeds": choices(speeds...),
		"App.globeSettingsError": "",
	}
}

func (g *globeMap) settingsCommands() map[string]Command {
	return map[string]Command{
		"App.setGlobeTheme":  oneNumber("a row", g.setTheme),
		"App.setGlobeColour": twoStrings("a colour's name and #rrggbb", g.setColour),
		"App.setGlobeRules":  oneString("#tag=#rrggbb; folder/=#rrggbb", g.setRules),
	}
}
