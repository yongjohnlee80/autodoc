package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/i18n"
)

// language_test.go: the six languages' gates (ADR 1792451618 §5), as AutoDB's: every id AutoDoc uses
// is in the English catalog (the inventory), every shipped language says every one of them (the
// coverage), golib's walk of the screen in each (mnemonics and collisions), and a switch relabels
// the screen in one frame.

var (
	qsTrIDs = regexp.MustCompile(`qsTrId\("([^"]+)"\)`)
	// a literal id, whole: one built with + (a command's, a theme's) is the generator's to hold
	goTrIDs  = regexp.MustCompile(`\b(?:tr|trf|Tr|Trf)\("(autodoc\.[^"]+)"\s*[,)]`)
	goMsgIDs = regexp.MustCompile(`\bMsg\("(autodoc\.[^"]+)"\)`)
)

// usedIDs are the catalog ids AutoDoc's QML and Go name literally, with where.
func usedIDs(t *testing.T) map[string]string {
	t.Helper()
	used := map[string]string{}
	add := func(re *regexp.Regexp, path string, b []byte) {
		for _, m := range re.FindAllSubmatch(b, -1) {
			used[string(m[1])] = path
		}
	}
	for _, root := range []string{"qml", "qmlgui"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(p) != ".qml" {
				return err
			}
			b, err := os.ReadFile(p)
			add(qsTrIDs, p, b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	gos, _ := filepath.Glob("*.go")
	for _, p := range gos {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		add(qsTrIDs, p, b) // QML written in Go (relationviews_gui.go)
		add(goTrIDs, p, b)
		add(goMsgIDs, p, b)
	}
	return used
}

// The inventory: every id the QML and the Go name is in autodoc_en.xml, so no screen ever shows a
// raw id.
func TestEveryIDIsInTheEnglishCatalog(t *testing.T) {
	en := readEnglish(t)
	used := usedIDs(t)
	if len(used) < 100 {
		t.Fatalf("only %d ids found: the scan reads nothing", len(used))
	}
	for id, where := range used {
		if _, ok := en[id]; !ok {
			t.Errorf("%s (%s) is not in %s", id, where, enCatalog)
		}
	}
}

// The catalogs are AutoDoc's languages: one file a language, each its tag's, each a language the
// build offers; English is there.
func TestTheCatalogsAreTheShippedLanguages(t *testing.T) {
	names, err := filepath.Glob("qml/i18n/autodoc_*.xml")
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, n := range names {
		tag := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "autodoc_"), ".xml")
		if !slices.Contains(languages, tag) {
			t.Errorf("%s is no language the build offers (language.go)", n)
		}
		tags = append(tags, tag)
	}
	if !slices.Contains(tags, "en") {
		t.Error("no English catalog")
	}
	s := &i18n.Set{}
	if err := s.LoadDir(qmlFiles, "i18n", "autodoc"); err != nil {
		t.Errorf("the catalogs do not load: %v", err)
	}
}

// The coverage: each shipped language other than English says every English id, or golib shows
// English in its place, a gap no one sees until a user meets it.
func TestEveryShippedLanguageCoversEveryID(t *testing.T) {
	en := readEnglish(t)
	s := &i18n.Set{}
	if err := s.LoadDir(qmlFiles, "i18n", "autodoc"); err != nil {
		t.Fatal(err)
	}
	for _, lang := range s.Languages() {
		if lang == i18n.English {
			continue
		}
		have := map[string]bool{}
		for _, id := range s.IDs(lang) {
			have[id] = true
		}
		var missing []string
		for id := range en {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			t.Errorf("%s lacks %d of English's %d ids, the first: %v", lang, len(missing), len(en), missing[:min(len(missing), 10)])
		}
	}
}

// golib's walk of the screen in every language: no translation of an id English lacks, no wrong
// mnemonic letter, no two rows of one menu on one key.
func TestEveryLanguageWalksTheScreen(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	decltest.CheckLanguages(t, h.options(Options{})...)
}

// koCatalog is a Korean catalog for the switch cell: the About dialog's title, a leader key, a
// chooser's row and a notice, as the five shipped catalogs will say them.
func koCatalog() fstest.MapFS {
	return fstest.MapFS{"i18n/autodoc_ko_KR.xml": {Data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<TS version="2.1" language="ko_KR"><context><name>autodoc</name>
<message id="autodoc.view.about.about_autodoc"><translation>AutoDoc 정보</translation></message>
<message id="autodoc.key.open_a_document"><translation>문서 열기</translation></message>
<message id="autodoc.choice.vim_modal"><translation>Vim (모드)</translation></message>
</context></TS>`)}}
}

// A switch relabels the screen in one frame, an open dialog included: a QML label (the About
// dialog's title), a card AutoDoc composes in Go (the leader's), a chooser's rows, and the menu's
// check; and back.
func TestALanguageSwitchRelabelsTheScreen(t *testing.T) {
	sess := NewSession("unused", nil)
	h := newHost(sess, Options{})
	s := decltest.RunWith(t, 100, 30, h.attach, append(h.options(Options{}),
		tuidecl.Translations(koCatalog(), "i18n", "autodoc"))...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	onLoop(r, func() bool { h.open("about"); return true })
	s.WaitForText(t, "about autodoc")
	onLoop(r, func() bool { h.setLanguage("ko_KR"); return true }) // Options › Language's row: the preference, applied
	s.WaitForText(t, "AutoDoc 정보")
	source := func(name string) qml.SpecValue {
		return onLoop(r, func() qml.SpecValue { v, _ := h.p.Tree().Source(name); return v })
	}
	if text := source("App.leaderText").Raw; !strings.Contains(text, "문서 열기") {
		t.Errorf("the leader card is not composed again in Korean:\n%s", text)
	}
	if m, ok := source("App.keymaps").Obj.(*tuidecl.ListModel); !ok || m.Data(tuidecl.Index{Row: 0}, "label").Raw != "Vim (모드)" {
		t.Errorf("the keymap chooser is not in Korean: %+v", source("App.keymaps"))
	}
	if source("App.language_ko_KR").Raw != "true" {
		t.Error("Options › Language's check did not move to 한국어")
	}
	onLoop(r, func() bool { h.setLanguage("en"); return true })
	s.WaitForText(t, "about autodoc")
}

// A stored language the build does not offer is English, and the store keeps it.
func TestAStoredLanguageTheBuildDoesNotOfferIsEnglish(t *testing.T) {
	var p prefs
	readRows(&p, map[string]any{prefLanguage: "fr"}, legacyPrefRows(nil, false))
	if p.language != "en" {
		t.Errorf("a stored %q reads as %q, want English", "fr", p.language)
	}
	readRows(&p, map[string]any{prefLanguage: "ko_KR"}, legacyPrefRows(nil, false))
	if p.language != "ko_KR" {
		t.Errorf("a stored ko_KR reads as %q", p.language)
	}
}
