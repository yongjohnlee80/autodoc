package tui

import (
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// crewFiles are the crew's own sources: everything they show a person goes through the catalog.
var crewFiles = []string{"crewhost.go", "crewinbox.go", "crewreply.go", "crewbell.go", "creworphans.go", "agentclaude.go"}

// TestTheCrewSpeaksThroughItsCatalog: no notice, toast, title or button in the crew's sources is a
// literal or a Sprintf (each is Tr or Trf of an autodoc.crew.* id), every id they and the agents'
// crew notices name is in all six catalogs, and the catalogs hold no crew id nothing names.
func TestTheCrewSpeaksThroughItsCatalog(t *testing.T) {
	literal := regexp.MustCompile(`Notify\("|Notify\(fmt\.|Notify\("[^"]*"\s*\+|\b(Title|Text|Yes|No|Other):\s*("|fmt\.)|\b(text|done):\s*fmt\.|Set\("App\.\w+",\s*fmt\.`)
	idRef := regexp.MustCompile(`"(autodoc\.crew\.[a-z_]+)"`)
	used := map[string]bool{}
	for _, f := range append(slices.Clone(crewFiles), "agentinstances.go") {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if slices.Contains(crewFiles, f) && literal.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d says something to a person outside the catalog: %s", f, i+1, strings.TrimSpace(line))
			}
			for _, m := range idRef.FindAllStringSubmatch(line, -1) {
				used[m[1]] = true
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("the crew names no catalog id")
	}
	inCatalog := regexp.MustCompile(`<message id="(autodoc\.crew\.[a-z_]+)">`)
	for _, lang := range []string{"en", "es", "ja_JP", "ko_KR", "pt_BR", "zh_CN"} {
		b, err := os.ReadFile("qml/i18n/autodoc_" + lang + ".xml")
		if err != nil {
			t.Fatal(err)
		}
		has := map[string]bool{}
		for _, m := range inCatalog.FindAllStringSubmatch(string(b), -1) {
			has[m[1]] = true
		}
		for id := range used {
			if !has[id] {
				t.Errorf("%s has no %s", lang, id)
			}
		}
		for id := range has {
			if !used[id] {
				t.Errorf("%s holds %s, which nothing names", lang, id)
			}
		}
	}
}

// saysMarked reports whether got is the marked test language's message id.
func saysMarked(t *testing.T, what string, got string, id string) {
	t.Helper()
	if !strings.HasPrefix(got, "⟪"+id+"⟫") {
		t.Errorf("%s: %q, want the catalog's %s", what, got, id)
	}
}

// TestTheInboxSpeaksTheCatalogsLanguage: in a test language, every inbox toast, notice and the
// reply's title are that language's: a request, a report, each kind of wait, its end, a reply sent,
// failed and closed, a dismissal refused, an agent elsewhere, the bell.
func TestTheInboxSpeaksTheCatalogsLanguage(t *testing.T) {
	w := newInboxWindow()
	w.marked = true
	attn := func(s, kind string) map[string]any {
		return map[string]any{"session": s, "codename": "tifa", "kind": kind, "text": "edit?"}
	}
	w.answers = []any{
		snapshot(1, "", []any{request("r1", "gold", "which db?", "s1"), report("m1", "lector", "done")},
			[]any{attn("s7", "permission"), attn("s8", "blocked"), attn("s9", "other")}),
		changes(2, "cleared"),
		snapshot(2, "", []any{request("r1", "gold", "which db?", "s1")}, nil),
	}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	saysMarked(t, "a request", w.shown["crew:r1"], "autodoc.crew.asks")
	saysMarked(t, "a report", w.notes[0], "autodoc.crew.reports")
	saysMarked(t, "a permission", w.shown["crew-attention:s7"], "autodoc.crew.waits_permission")
	saysMarked(t, "a blocked agent", w.shown["crew-attention:s8"], "autodoc.crew.waits_blocked")
	saysMarked(t, "another wait", w.shown["crew-attention:s9"], "autodoc.crew.waits")
	w.step(t)
	w.step(t)
	saysMarked(t, "a wait's end", w.ended["crew-attention:s7"], "autodoc.crew.no_longer_waits")
	w.clicks["crew:r1"]()
	saysMarked(t, "the reply's title", w.set["App.replyTitle"].(string), "autodoc.crew.asks")
	w.verbAnswers["inbox.reply"] = []error{io.ErrUnexpectedEOF, nil}
	w.verbAnswers["inbox.dismiss"] = []error{io.ErrUnexpectedEOF}
	x.replySend("postgres")
	w.step(t)
	w.step(t)
	saysMarked(t, "a reply not sent", w.notes[len(w.notes)-1], "autodoc.crew.reply_failed")
	w.clicks["crew:r1"]()
	x.replySend("postgres")
	w.step(t)
	saysMarked(t, "a reply sent", w.notes[len(w.notes)-1], "autodoc.crew.replied")
	w.clicks["crew:r1"]()
	x.replyDismiss()
	w.step(t)
	saysMarked(t, "a dismissal refused", w.notes[len(w.notes)-1], "autodoc.crew.dismiss_failed")
	x.raiseAttention("s2", "jarvis")
	w.step(t)
	saysMarked(t, "an agent elsewhere", w.notes[len(w.notes)-1], "autodoc.crew.other_window")
	x.toggleBell()
	saysMarked(t, "the bell on", w.notes[len(w.notes)-1], "autodoc.crew.bell_on")
	x.toggleBell()
	saysMarked(t, "the bell off", w.notes[len(w.notes)-1], "autodoc.crew.bell_off")
}

// TestTheOrphanOfferSpeaksTheCatalogsLanguage: in a test language, the orphan offer's card (its
// title, text and buttons) and its outcome are that language's.
func TestTheOrphanOfferSpeaksTheCatalogsLanguage(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost(), marked: true}
	w.d.orphans = []any{orphan("autodoc:dead1", 3, "gold")}
	h := newCrewHost(w, t.TempDir())
	h.Connected()
	q := w.card.current
	saysMarked(t, "the card's title", q.Title, "autodoc.crew.orphan_title")
	saysMarked(t, "the card's text", q.Text, "autodoc.crew.orphan_text")
	saysMarked(t, "Adopt", q.Yes, "autodoc.crew.orphan_adopt")
	saysMarked(t, "Keep", q.No, "autodoc.crew.orphan_keep")
	saysMarked(t, "Discard", q.Other, "autodoc.crew.orphan_discard")
	w.card.accepted()
	saysMarked(t, "the adoption", w.notices[len(w.notices)-1], "autodoc.crew.adopted")
}
