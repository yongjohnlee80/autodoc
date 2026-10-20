package tui

import (
	"html"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// language_catalog_test.go: the English catalog's text that AutoDoc keeps in Go tables — the keys
// and cards (keys.go), the commands (the catalog) and help (help.go) — generated from them, as the
// cards' QML is (TestKeysAreGenerated): go test ./tui -run TestTheEnglishCatalogHoldsTheGoTables -update

const enCatalog = "qml/i18n/autodoc_en.xml"

// generatedPrefixes are the ids this test owns in the English catalog.
var generatedPrefixes = []string{"autodoc.key.", "autodoc.card.", "autodoc.cmd.", "autodoc.help.", "autodoc.choice.", "autodoc.theme."}

// goTableText is every id the Go tables say, with its English.
func goTableText(t *testing.T) map[string]string {
	t.Helper()
	h := newHost(NewSession("unused", nil), Options{})
	fs := h.features()
	r, err := newRegistry(fs...)
	if err != nil {
		t.Fatal(err)
	}
	k := keyCatalog(fs)
	out := map[string]string{}
	put := func(id, english string) {
		if english == "" {
			return
		}
		if was, ok := out[id]; ok && was != english {
			t.Errorf("%s says both %q and %q: give one a longer slug", id, was, english)
		}
		out[id] = english
	}
	for _, key := range k.keys {
		put("autodoc.key."+slugOf(key.Does), key.Does)
	}
	for _, c := range k.cards {
		put("autodoc.card."+c.ID+".title", c.Title)
		put("autodoc.card."+c.ID+".help", c.Help)
		for _, g := range append(slices.Concat(c.Groups...), groupMore) {
			put("autodoc.card.group."+slugOf(g), g)
		}
	}
	for _, e := range r.catalog.entries() {
		if e.Source != SourceBuiltin && e.Source != SourceFeature {
			continue // a script's or a plugin's: its own words
		}
		put("autodoc.cmd."+e.ID, e.Title)
		put("autodoc.cmd."+e.ID+".help", e.Help)
	}
	for _, labels := range choiceLabels {
		for _, l := range labels {
			put("autodoc.choice."+slugOf(l), l)
		}
	}
	for n, l := range themeMenuLabels {
		put("autodoc.theme."+n, l)
	}
	put("autodoc.help.keys", helpKeys)
	put("autodoc.help.rest", helpRest)
	put("autodoc.help.commands", helpCommandsHead)
	for _, g := range helpGroups {
		put("autodoc.help.group."+slugOf(g.head), g.head)
	}
	return out
}

var tsMessage = regexp.MustCompile(`(?s)<message id="([^"]+)">\s*<source>(.*?)</source>`)

// readEnglish is the English catalog's ids and their source text.
func readEnglish(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(enCatalog)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range tsMessage.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	return out
}

// writeEnglish writes the English catalog: every id, sorted, its English as source and translation.
func writeEnglish(t *testing.T, msgs map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!DOCTYPE TS>\n<TS version=\"2.1\" language=\"en\">\n<context>\n    <name>autodoc</name>\n")
	for _, id := range slices.Sorted(maps.Keys(msgs)) {
		e := html.EscapeString(msgs[id])
		e = strings.NewReplacer("&#39;", "'", "&#34;", "\"").Replace(e) // as the catalog writes them
		b.WriteString("    <message id=\"" + id + "\">\n        <source>" + e + "</source>\n        <translation>" + e + "</translation>\n    </message>\n")
	}
	b.WriteString("</context>\n</TS>\n")
	if err := os.WriteFile(enCatalog, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func generated(id string) bool {
	return slices.ContainsFunc(generatedPrefixes, func(p string) bool { return strings.HasPrefix(id, p) })
}

// The English catalog holds what the Go tables say, each id once and as they say it, and nothing
// they no longer say.
func TestTheEnglishCatalogHoldsTheGoTables(t *testing.T) {
	want := goTableText(t)
	have := readEnglish(t)
	if *updateKeys {
		maps.DeleteFunc(have, func(id, _ string) bool { return generated(id) })
		maps.Copy(have, want)
		writeEnglish(t, have)
		return
	}
	for id, english := range want {
		if got, ok := have[id]; !ok || got != english {
			t.Errorf("%s: the catalog says %q, the tables %q (go test ./tui -run %s -update)", id, got, english, t.Name())
		}
	}
	for id := range have {
		if generated(id) && want[id] == "" {
			t.Errorf("%s is in the catalog, and no table says it (go test ./tui -run %s -update)", id, t.Name())
		}
	}
}
