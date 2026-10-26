package tui

import (
	"fmt"
	"html"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yongjohnlee80/autodoc/docs"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
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
	// an id kept in a table (a capability's, a kind's): whole, never a prefix ending in _ or .
	goTableIDs = regexp.MustCompile(`"(autodoc\.(?:notice|text|question)\.[a-z0-9_]+\.[a-z0-9_]*[a-z0-9])"`)
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
		add(goTableIDs, p, b)
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
// mnemonic letter, no two rows of one menu on one key. The GUI build walks a window's screen, its
// own menus (the GlobeMap's, the Mindmap's) with it.
func TestEveryLanguageWalksTheScreen(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{GUI: wholeTables})
	decltest.CheckLanguages(t, h.options(Options{GUI: wholeTables})...)
}

// koCatalog is a Korean catalog for the switch cell: the About dialog's title, a leader key, a
// chooser's row, a feature's menu row and the status line's search, as the shipped catalogs say them.
func koCatalog() fstest.MapFS {
	return fstest.MapFS{"i18n/autodoc_ko_KR.xml": {Data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<TS version="2.1" language="ko_KR"><context><name>autodoc</name>
<message id="autodoc.view.about.about_autodoc"><translation>AutoDoc 정보</translation></message>
<message id="autodoc.key.open_a_document"><translation>문서 열기</translation></message>
<message id="autodoc.choice.vim_modal"><translation>Vim (모드)</translation></message>
<message id="autodoc.text.plugincmds.no_plugins_yet"><translation>아직 플러그인이 없습니다</translation></message>
<message id="autodoc.text.progress.lexical_search"><translation>어휘 검색</translation></message>
<message id="autodoc.text.progress.semantic_search"><translation>의미 검색</translation></message>
</context></TS>`)}}
}

// A switch relabels the screen in one frame, an open dialog included: a QML label (the About
// dialog's title), a card AutoDoc composes in Go (the leader's), a chooser's rows, the status line,
// a feature's menu rows, and the menu's check; and back.
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
	if l := source("App.semanticLabel").Raw; l != "어휘 검색" && l != "의미 검색" {
		t.Errorf("the status line's search is not in Korean: %q", l)
	}
	if l := onLoop(r, func() string { return h.plugins.pluginRows.Data(tuidecl.Index{Row: 0}, "label").Raw }); l != "아직 플러그인이 없습니다" {
		t.Errorf("the Plugins menu (a feature's text, LanguageSink) is not in Korean: %q", l)
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

// trf fills a sentence in one pass: an argument that says %1 or %2 itself (a path, a command, a
// search) is shown as written, never filled again; a %N past the arguments stays, as Qt's arg.
func TestFillIsOnePass(t *testing.T) {
	for _, c := range []struct {
		s    string
		args []any
		want string
	}{
		{"%1 is taken by %2", []any{"%2", "x"}, "%2 is taken by x"},
		{"open %1 in %2", []any{"a", "%1%2"}, "open a in %1%2"},
		{"settling %1%", []any{40}, "settling 40%"},
		{"%2 before %1", []any{"a", "b"}, "b before a"},
		{"%10 of %1", []any{"a"}, "a0 of a"},
		{"%3 stays", []any{"a"}, "%3 stays"},
		{"no arguments: %1", nil, "no arguments: %1"},
	} {
		if got := fill(c.s, c.args...); got != c.want {
			t.Errorf("fill(%q, %v) = %q, want %q", c.s, c.args, got, c.want)
		}
	}
}

// pseudoCatalog is ja_JP saying every English message marked, ⟦English⟧ (marks no message holds), each %N and mnemonic kept:
// text a switch composed again reads marked, and English left on screen reads bare.
func pseudoCatalog(t *testing.T) fstest.MapFS {
	t.Helper()
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<TS version=\"2.1\" language=\"ja_JP\"><context><name>autodoc</name>\n")
	for id, english := range readEnglish(t) {
		b.WriteString(`<message id="` + id + `"><translation>` + html.EscapeString("⟦"+english+"⟧") + "</translation></message>\n")
	}
	b.WriteString("</context></TS>\n")
	return fstest.MapFS{"i18n/autodoc_ja_JP.xml": {Data: []byte(b.String())}}
}

var (
	marked     = regexp.MustCompile(`⟦[^⟦⟧]*⟧`)
	argPattern = regexp.MustCompile(`%[1-9][0-9]?`)
)

// bareEnglish is the English catalog text in s outside every ⟦…⟧: the words of AutoDoc's own
// sentences, each run between its %N at least 8 bytes and two words, so a name or a key is none.
func bareEnglish(s string, english []string) []string {
	for {
		t := marked.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	var found []string
	for _, e := range english {
		if strings.Contains(s, e) {
			found = append(found, e)
		}
	}
	return found
}

// englishRuns are the English catalog's sentence runs bareEnglish looks for.
func englishRuns(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var runs []string
	for _, e := range readEnglish(t) {
		for _, run := range argPattern.Split(strings.ReplaceAll(e, "&", ""), -1) {
			run = strings.TrimSpace(run)
			if len(run) >= 8 && strings.Contains(run, " ") && !seen[run] {
				seen[run] = true
				runs = append(runs, run)
			}
		}
	}
	return runs
}

// notCatalogText are the sources whose words are not the catalog's: the tutorial's page (docs, in
// each language's own pages) and the dock's titles, which name widgets for Lua and a tooltip (ADR
// 1792451618 Q3b).
var notCatalogText = map[string]bool{"App.tutorialText": true, "App.dock": true}

// A switch leaves no English on screen: in a language that says every message marked, no App.*
// value and no row of a list model the screen shows holds a run of AutoDoc's English unmarked. The
// text composed in Go is composed again (languageChanged, each feature's LanguageSink), not only the
// QML labels golib relabels itself.
func TestASwitchLeavesNoEnglishOnScreen(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{GUI: wholeTables})
	s := decltest.RunWith(t, 120, 40, h.attach, append(h.options(Options{GUI: wholeTables}),
		tuidecl.Translations(pseudoCatalog(t), "i18n", "autodoc"))...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	runs := englishRuns(t)
	onLoop(r, func() bool { // a row a model composes in Go: a todo variable set per machine, none here
		for _, f := range h.reg.features {
			if td, ok := f.(*todos); ok {
				td.vars.Reset([]map[string]any{{"name": "owner", "machine": true}})
			}
		}
		return true
	})
	onLoop(r, func() bool { h.setLanguage("ja_JP"); return true })
	s.WaitFor(t, "the switch", func(string) bool { return onLoop(r, func() bool { return h.language == "ja_JP" }) })
	onLoop(r, func() bool { return true }) // F1 is made anew on the loop's next turn (commandsChanged)
	bare := onLoop(r, func() map[string][]string {
		// a script's or a plugin's command is in its own words, shown as written (trOr): AutoDoc's
		// bundled scripts' too
		var own []string
		for _, e := range h.cat.entries() {
			if e.Source != SourceBuiltin && e.Source != SourceFeature {
				own = append(own, e.Title, e.Help)
			}
		}
		out := map[string][]string{}
		for name := range h.reg.state {
			if notCatalogText[name] {
				continue
			}
			v, ok := h.p.Tree().Source(name)
			if !ok {
				continue
			}
			raw := v.Raw
			for _, words := range own {
				if words != "" {
					raw = strings.ReplaceAll(raw, words, "")
				}
			}
			if found := bareEnglish(raw, runs); len(found) > 0 {
				out[name] = found
			}
			m, ok := v.Obj.(*tuidecl.ListModel)
			if !ok {
				continue
			}
			for i := range m.Len() {
				for _, role := range m.Roles() {
					if role == "key" {
						continue // a row's key is English by design, and never shown
					}
					if found := bareEnglish(m.Data(tuidecl.Index{Row: i}, role).Raw, runs); len(found) > 0 {
						out[fmt.Sprintf("%s[%d].%s", name, i, role)] = found
					}
				}
			}
		}
		return out
	})
	for _, name := range slices.Sorted(maps.Keys(bare)) {
		t.Errorf("%s still says, in English: %q", name, bare[name])
	}
}

// sideBySide lines its columns up in cells, as the screen draws them: a CJK character is two cells,
// so a column after a CJK one starts at the same cell on every row.
func TestCardColumnsLineUpInCells(t *testing.T) {
	left := []string{"f  파일", "g  줄로 이동", "s  保存する", "x  打开"}
	right := []string{"a  one", "b  two", "c  three", "d  four"}
	text := sideBySide([][]string{left, right})
	want := 0
	for _, l := range left {
		want = max(want, tuicore.StringWidth(l))
	}
	want += 2
	for i, line := range strings.Split(text, "\n") {
		at := strings.Index(line, right[i])
		if at < 0 {
			t.Fatalf("row %d lacks its second column %q: %q", i, right[i], line)
		}
		if got := tuicore.StringWidth(line[:at]); got != want {
			t.Errorf("row %d's second column starts at cell %d, want %d:\n%s", i, got, want, text)
		}
	}
}

// Every card, in every shipped language, fits the smallest screen's card (80 × 20 less its frame):
// its lines measured in cells.
func TestEveryCardFitsInEveryLanguage(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{GUI: wholeTables})
	k := keyCatalog(h.features())
	s := &i18n.Set{}
	if err := s.LoadDir(qmlFiles, "i18n", "autodoc"); err != nil {
		t.Fatal(err)
	}
	for _, lang := range s.Languages() {
		k.tr = func(id, english string) string {
			if t, ok := s.Lookup(lang, id); ok {
				return t
			}
			return english
		}
		for _, c := range k.cards {
			text := k.cardText(c.Groups, k.on(c.ID))
			for _, line := range strings.Split(text, "\n") {
				if n := tuicore.StringWidth(line); n > 76 {
					t.Errorf("%s: %s's card has a line of %d cells, wider than the 80 × 20 screen's card:\n%s", lang, c.ID, n, line)
				}
			}
		}
	}
}

// codeFence is a fenced block: its language and its body.
var codeFence = regexp.MustCompile("(?ms)^```(\\S*)\\n(.*?)^```")

// tutorialCode is a tutorial page's code, every fence's body but a diagram's: what a translation
// keeps as English's, so the Lua it shows runs as English's does (TestTheTutorialsLuaRuns).
func tutorialCode(page string) []string {
	var code []string
	for _, m := range codeFence.FindAllStringSubmatch(page, -1) {
		if m[1] != "mermaid" {
			code = append(code, m[2])
		}
	}
	return code
}

// Every language the build offers has the whole tutorial, translated, its code English's; a folder
// in docs/tutorials/ is such a language's, holding English's pages and no other.
func TestEveryLanguageHasTheWholeTutorial(t *testing.T) {
	english, err := fs.Glob(docs.Tutorials, "tutorials/[0-9]*.md")
	if err != nil || len(english) == 0 {
		t.Fatalf("no English tutorial: %v", err)
	}
	dirs, err := fs.ReadDir(docs.Tutorials, "tutorials")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if d.IsDir() && (d.Name() == "en" || !slices.Contains(languages, d.Name())) {
			t.Errorf("docs/tutorials/%s is no translation the build offers (language.go)", d.Name())
		}
	}
	for _, tag := range languages[1:] {
		names, _ := fs.Glob(docs.Tutorials, path.Join("tutorials", tag, "*.md"))
		if len(names) != len(english) {
			t.Errorf("%s has %d tutorial pages; English has %d", tag, len(names), len(english))
		}
		for i, n := range english {
			b, err := fs.ReadFile(docs.Tutorials, path.Join("tutorials", tag, path.Base(n)))
			switch {
			case err != nil:
				t.Errorf("%s lacks %s", tag, path.Base(n))
			case string(b) == tutorialPages[i]:
				t.Errorf("%s's %s is English's", tag, path.Base(n))
			case !slices.Equal(tutorialCode(string(b)), tutorialCode(tutorialPages[i])):
				t.Errorf("%s's %s shows code other than English's", tag, path.Base(n))
			}
		}
	}
}

// The tutorial is in the UI's language, an open one turned to it by a switch, at the same page.
func TestTheTutorialFollowsTheLanguage(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d) // started: About at start has come and gone, so it covers nothing here
	h, s := r.h, r.s
	heading := func(tag string) string {
		first, _, _ := strings.Cut(tutorialBooks[tag][1], "\n")
		return strings.TrimPrefix(first, "# ")
	}
	onLoop(r, func() bool { h.openTutorial(); h.tutorialStep(1); return true })
	s.WaitForText(t, heading("en"))
	onLoop(r, func() bool { h.setLanguage("ja_JP"); return true })
	s.WaitForText(t, heading("ja_JP"))
	if got := onLoop(r, func() int { return h.tutorialAt }); got != 1 {
		t.Errorf("the switch turned the tutorial to page %d, want 2", got+1)
	}
	onLoop(r, func() bool { h.setLanguage("en"); return true })
	s.WaitForText(t, heading("en"))
}

// A page a language lacks is English's, in its place; and a language with no book (none chosen yet)
// reads English's.
func TestATutorialPageALanguageLacksIsEnglishs(t *testing.T) {
	books := tutorialBooksOf(fstest.MapFS{
		"tutorials/01-a.md":       {Data: []byte("# A\n")},
		"tutorials/02-b.md":       {Data: []byte("# B\n")},
		"tutorials/ko_KR/01-a.md": {Data: []byte("# 가\n")},
	})
	if got := books["ko_KR"]; !slices.Equal(got, []string{"# 가\n", "# B\n"}) {
		t.Errorf("ko_KR's book is %q, want its own page 1 and English's page 2", got)
	}
	if got := books["ja_JP"]; !slices.Equal(got, []string{"# A\n", "# B\n"}) {
		t.Errorf("ja_JP's book is %q, want English's", got)
	}
	if got := (&Host{}).tutorial(); !slices.Equal(got, tutorialPages) {
		t.Error("a Host with no language does not read English's tutorial")
	}
}

// The embedding setup is in the language chosen: the dialog's strings come from the shipped catalogs
// like every other dialog's. (A table's column titles are literals in golib's TableViewColumn, in
// every dialog: they are not this cell's.)
func TestTheSetupDialogFollowsTheLanguage(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	s := decltest.RunWith(t, 120, 40, h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	onLoop(r, func() bool {
		h.setLanguage("ko_KR")
		h.set("App.embedSetupReady", true) // the table shows once a check has answered
		h.open("embeddingSetup")
		return true
	})
	s.WaitForText(t, "의미 검색 설정")
	s.WaitForText(t, "Enter로 워크스페이스를 체크하거나 해제합니다")
	if sc := s.String(); strings.Contains(sc, "Set up semantic search") || strings.Contains(sc, "Enter ticks") {
		t.Errorf("the setup's English in the Korean screen:\n%s", sc)
	}
}
