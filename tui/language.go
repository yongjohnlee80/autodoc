package tui

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/i18n"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// LANGUAGES — AutoDoc's UI in six languages (docs/decisions/editor.md#six-languages), on golib's
// localization: AutoDoc's catalogs (tui/qml/i18n/autodoc_<tag>.xml) over golib's, a QML label's
// qsTrId and a Go row's or notice's tuicore.Msg resolved by golib in the App's language, which one
// store-wide preference (tui.language) chooses. A switch relabels everything in one frame
// (App.ApplyLanguage); the text AutoDoc composes in Go is composed again (languageChanged). A
// stored language this build does not offer is English, and the store keeps it (prefspec's
// readRows).

// prefLanguage is the UI's language: store-wide, as the theme is, and the same in both builds.
const prefLanguage = "tui.language"

// languages are the languages AutoDoc ships, golib's tags, English first.
var languages = []string{"en", "ko_KR", "ja_JP", "zh_CN", "es", "pt_BR"}

// languageNames are each language in its own script: the rows every language reads alike, never
// translated.
var languageNames = map[string]string{
	"en":    "English",
	"ko_KR": "한국어",
	"ja_JP": "日本語",
	"zh_CN": "中文",
	"es":    "Español",
	"pt_BR": "Português (Brasil)",
}

// languageLabels are languageNames in languages' order, for the Preferences dialog's chooser.
func languageLabels() []string {
	out := make([]string, len(languages))
	for i, tag := range languages {
		out[i] = languageNames[tag]
	}
	return out
}

// languageChecks are Options › Language's checks: App.language_<tag>, the one in use true.
func languageChecks(tag string) map[string]any {
	m := make(map[string]any, len(languages))
	for _, l := range languages {
		m["App.language_"+l] = l == tag
	}
	return m
}

// useLanguage shows the UI in tag, in one frame: golib relabels every message, and the text
// AutoDoc composes in Go is composed again. It runs on the loop.
func (h *Host) useLanguage(tag string) {
	if !slices.Contains(languages, tag) {
		tag = "en"
	}
	if tag == h.language {
		return
	}
	h.language = tag
	if h.p != nil {
		h.p.App().ApplyLanguage(tag)
	}
	h.languageChanged()
}

// setLanguage is Options › Language's row: tag, kept.
func (h *Host) setLanguage(tag string) {
	if !slices.Contains(languages, tag) {
		h.say(h.trf("autodoc.notice.language.no_language", strconv.Quote(tag)))
		return
	}
	h.setPref(prefLanguage, tag, func(p *prefs) { p.language = tag })
}

// tr is a message's text in the App's language now, for text AutoDoc composes in Go: a notice and
// the status line keep the language they were composed in (the next replaces them), and what stays
// on screen is composed again by languageChanged. Before the program, it is English.
func (h *Host) tr(id string) string {
	if h.p == nil {
		return englishText(id)
	}
	return h.p.App().Translate(tuicore.Msg(id))
}

// trf is tr with its %1, %2 … filled with args, as Qt's QString::arg: a sentence is translated whole
// and then filled, so no language sees it assembled from fragments.
func (h *Host) trf(id string, args ...any) string { return fill(h.tr(id), args...) }

// argRef is a %1 … %99 in a message, which fill fills.
var argRef = regexp.MustCompile(`%[1-9][0-9]?`)

// fill is s with each %N its argument, in one pass, so an argument that says "%1" itself (a path, a
// command, a search) is shown as it is. A %N past the arguments stays, but for a two-digit one whose
// first digit names one ("%10" with one argument): the argument, then the digit, as Qt's arg.
func fill(s string, args ...any) string {
	if len(args) == 0 {
		return s
	}
	return argRef.ReplaceAllStringFunc(s, func(ref string) string {
		if n, _ := strconv.Atoi(ref[1:]); n <= len(args) {
			return fmt.Sprint(args[n-1])
		}
		if n := int(ref[1] - '0'); len(ref) == 3 && n <= len(args) {
			return fmt.Sprint(args[n-1]) + ref[2:]
		}
		return ref
	})
}

// englishCatalog is autodoc_en.xml, for text composed before the program has its App.
var englishCatalog = sync.OnceValue(func() *i18n.Set {
	s := &i18n.Set{}
	if err := s.LoadDir(qmlFiles, "i18n", "autodoc"); err != nil {
		panic("tui: the English catalog: " + err.Error()) // embedded: a test catches it first
	}
	return s
})

// englishText is id's English, or id when the catalog has none.
func englishText(id string) string {
	if t, ok := englishCatalog().Lookup(i18n.English, id); ok {
		return t
	}
	return id
}

// wording is a catalog's text for a function that composes it: core{h} (or a feature's Core) in the
// UI's language, or inEnglish.
type wording interface {
	Tr(id string) string
	Trf(id string, args ...any) string
}

// inEnglish is wording in the English catalog's words, filled as trf fills: for text no screen
// shows (a test's, a script's, an agent's task).
type inEnglish struct{}

func (inEnglish) Tr(id string) string               { return englishText(id) }
func (inEnglish) Trf(id string, args ...any) string { return fill(englishText(id), args...) }

// trOr is id's text in the language now, when the English catalog holds id as english: AutoDoc's own
// text. Anything else is shown as written: a plugin's or a script's text, which no catalog holds, or
// AutoDoc's text changed since its catalog was generated (keys_test.go holds them together).
func (h *Host) trOr(id, english string) string {
	if t, ok := englishCatalog().Lookup(i18n.English, id); !ok || t != english {
		return english
	}
	return h.tr(id)
}

// withLabelMessage is row with its label a catalog message, when the English catalog holds label
// as one of AutoDoc's menu rows: golib's Menu then fills it in the UI's language, its key English's.
// A name (a buffer's, a tab's, a plugin's) is shown as written.
func withLabelMessage(row widget.MenuItemModel, label string) widget.MenuItemModel {
	id := menuLabelID(label)
	if t, ok := englishCatalog().Lookup(i18n.English, id); ok && t == label {
		row.LabelMsg = tuicore.Msg(id)
	}
	return row
}

// menuLabelID is a menu row's catalog id: its words and, when it has one, its key, so "&Close" and
// "Close" are two rows.
func menuLabelID(label string) string {
	id := "autodoc.gomenu." + slugOf(label)
	if _, key, _ := tuicore.ParseMnemonic(label); key != 0 {
		id += "_" + strings.ToLower(string(key))
	}
	return id
}

// slugOf is s as an id's last part: its words, lower-case, joined by "_", at most 48 bytes.
func slugOf(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := strings.Join(words, "_")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "_")
	}
	return cmp.Or(out, "text")
}

// choiceLabels are the choosers' rows in AutoDoc's words, by their App.* source: a ComboBox's rows
// are text, so they are composed in the UI's language (choiceState), their keys English.
var choiceLabels = map[string][]string{
	"App.keymaps":           keymapLabels,
	"App.edges":             edges,
	"App.yesNo":             {"yes", "no"},
	"App.openAsChoices":     openAsLabels,
	"App.diskReloadChoices": diskReloadLabels,
	"App.indentMarkModes":   indentMarkLabels,
	"App.corners":           cornerLabels,
	"App.toastSeconds":      {"1 s", "2 s", "3 s", "4 s", "5 s", "6 s", "7 s", "8 s", "9 s", "10 s"},
	"App.policies":          policyChoices,
	"App.destinations":      destinationLabels,
	"App.vectorIndexes":     indexLabels,
	"App.sourceEngines":     sourceLabels,
}

// featureChoiceLabels are the choosers a feature owns (the GlobeMap's and the Mindmap's, in the GUI
// build), by their App.* source: the feature sets them through choiceModel, and sets them again on a
// switch (LanguageSink); their rows are catalog text as choiceLabels' are.
var featureChoiceLabels = relationViewChoices()

// choiceState is the choosers' rows in the UI's language.
func (h *Host) choiceState() map[string]any {
	m := make(map[string]any, len(choiceLabels))
	for src := range choiceLabels {
		m[src] = choiceModel(core{h}, src)
	}
	return m
}

// choiceModel is the chooser src's rows (choiceLabels) in w's language, for a feature that sets them
// again: each key its English, each label its catalog text (autodoc.choice.<slug>).
func choiceModel(w wording, src string) *tuidecl.ListModel {
	labels, ok := choiceLabels[src]
	if !ok {
		labels = featureChoiceLabels[src]
	}
	rows := make([]rowOf, len(labels))
	for i, l := range labels {
		rows[i] = rowOf{"key": l, "label": w.Tr("autodoc.choice." + slugOf(l))}
	}
	model := tuidecl.NewListModel("key", "label")
	model.Reset(rows)
	return model
}

// languageChanged composes again what AutoDoc composes in Go from catalog text and keeps on
// screen: the cards, the choosers, help, the command line's help, the titles and the status line,
// and each feature's own (LanguageSink). A dialog composed as it opens speaks the new language when
// it opens next: the language is chosen in a menu or in Preferences, with no other dialog open.
func (h *Host) languageChanged() {
	if h.p == nil {
		return
	}
	for name, text := range h.cardState() {
		h.set(name, text)
	}
	for name, model := range h.choiceState() {
		h.set(name, model)
	}
	h.commandsChanged()
	h.cat.syncAll()
	for name, text := range h.languageState() {
		h.set(name, text)
	}
	if h.file != nil { // the page's title and its badges on the status line
		h.showTitle()
		h.setDirty(h.file.dirty)
	}
	h.showSemantic()
	h.syncStages(false) // the Rerank box names its ranker
	h.showDiagnostics(h.fmDiagnostics)
	h.reg.languageChanged()
	if len(tutorialPages) > 0 {
		h.showTutorialPage() // its title and Next, should it be open
	}
}
