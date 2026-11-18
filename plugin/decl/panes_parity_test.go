package decl

import (
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/autodoc/plugin/card"
)

// THE SAME KEYS IN BOTH KITS: one key sequence, run through plugin/card's panes and plugin/decl's,
// leaves the same text in the same pane after every step, and F8 shows the same pane alone.

// cardHost is a card.Host keeping the latest frame.
type cardHost struct {
	mu   sync.Mutex
	last *plugin.Frame
}

func (h *cardHost) Frame(f *plugin.Frame) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = f
	return nil
}

func (h *cardHost) Title(string) error { return nil }

func (h *cardHost) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		return ""
	}
	return text(h.last)
}

// kitStep is one step of the sequence: its keys, then what each pane's field holds after it, and
// which titles show (nil: not checked).
type kitStep struct {
	name   string
	keys   []plugin.Key
	one    string
	two    string
	titles []string
}

func typed(s string) []plugin.Key {
	var ks []plugin.Key
	for _, r := range s {
		ks = append(ks, plugin.Key{Key: string(r), Text: string(r)})
	}
	return append(ks, plugin.Key{Key: "Enter"})
}

func keys(groups ...[]plugin.Key) []plugin.Key {
	var out []plugin.Key
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func one(k plugin.Key) []plugin.Key { return []plugin.Key{k} }

// paritySteps start with the first pane holding the keys, in both kits.
var paritySteps = []kitStep{
	{"typed in the first pane", typed("p"), "p", "", []string{"One", "Two"}},
	{"F7, then typed", keys(one(fkey("F7", false)), typed("q")), "p", "q", nil},
	{"F7 wraps to the first", keys(one(fkey("F7", false)), typed("r")), "pr", "q", nil},
	{"Shift+F7 to the second", keys(one(fkey("F7", true)), typed("s")), "pr", "qs", nil},
	{"Shift+F7 wraps to the first", keys(one(fkey("F7", true)), typed("t")), "prt", "qs", nil},
	{"Tab stays in the pane", keys(one(plugin.Key{Key: "Tab"}), typed("u")), "prtu", "qs", nil},
	{"Shift+Tab stays in the pane", keys(one(plugin.Key{Key: "Tab", Shift: true}), typed("v")), "prtuv", "qs", nil},
	{"F7, F8: the second alone", keys(one(fkey("F7", false)), one(fkey("F8", false)), typed("w")), "prtuv", "qsw", []string{"Two"}},
	{"F8 restores both", keys(one(fkey("F8", false)), typed("x")), "prtuv", "qswx", []string{"One", "Two"}},
	{"F7 in the restored panes", keys(one(fkey("F7", false)), typed("y")), "prtuvy", "qswx", []string{"One", "Two"}},
	// F7 with a pane maximized steps only the panes shown: the maximized one alone keeps the keys
	{"F8, then F7 while maximized", keys(one(fkey("F8", false)), one(fkey("F7", false)), typed("z")), "prtuvyz", "qswx", []string{"One"}},
	{"F8 restores both again", one(fkey("F8", false)), "prtuvyz", "qswx", []string{"One", "Two"}},
	// a frame after the restore: golib gives a pane just shown the keys once it is laid out
	{"F7 after the restore", keys(one(fkey("F7", false)), typed("0")), "prtuvyz", "qswx0", []string{"One", "Two"}},
}

// showsTitles reports which of One and Two a frame's text holds as a pane's title.
func showsTitles(s string) []string {
	var out []string
	for _, t := range []string{"One", "Two"} {
		if strings.Contains(s, t) {
			out = append(out, t)
		}
	}
	return out
}

// TestBothKitsAnswerThePaneKeysAlike: F7, Shift+F7, F8, Tab and Shift+Tab run through a native
// card of two panes and a decl card of two panes, a field in each, leave the same text in the same
// pane after every step, and F8 shows the same pane alone. The keys are the kits' contract
// (docs/plugins.md's table), so a difference is a bug in one of them.
func TestBothKitsAnswerThePaneKeysAlike(t *testing.T) {
	// the native kit
	var nOne, nTwo card.Input
	nOne.OnEnter, nTwo.OnEnter = func() {}, func() {} // Enter keeps the field, as a decl TextField's does
	root := &card.Split{
		A: card.Pane{Title: "One", Page: func() card.Page { return card.Page{Body: []card.Block{&nOne}} }},
		B: card.Pane{Title: "Two", Page: func() card.Page { return card.Page{Body: []card.Block{&nTwo}} }},
	}
	nc := card.NewPanes(root, card.Options{Modal: true})
	nh := &cardHost{}
	nc.Attach(nh, 60, 10, themeA)

	// the decl kit
	dc, dh, got := panesCard(t, `Panes { Pane { title: "One"; `+field("a")+` } Pane { title: "Two"; `+field("b")+` } }`, 60, 10)
	lastHas(t, dh, "Two")
	held := func() (string, string) { // each field's text, as its Enter last said
		var a, b string
		for _, g := range got() {
			switch {
			case strings.HasPrefix(g, "a="):
				a = g[2:]
			case strings.HasPrefix(g, "b="):
				b = g[2:]
			}
		}
		return a, b
	}

	for _, st := range paritySteps {
		for _, k := range st.keys {
			nc.Key(k)
			dc.Key(k)
		}
		if a, b := nOne.Value(), nTwo.Value(); a != st.one || b != st.two {
			t.Fatalf("%s: the native kit's panes hold %q and %q; want %q and %q", st.name, a, b, st.one, st.two)
		}
		waitFor(t, st.name+" in the decl kit", func() bool { a, b := held(); return a == st.one && b == st.two })
		if st.titles == nil {
			continue
		}
		if got := showsTitles(nh.text()); strings.Join(got, ",") != strings.Join(st.titles, ",") {
			t.Fatalf("%s: the native kit shows %v; want %v", st.name, got, st.titles)
		}
		waitFor(t, st.name+": the decl kit shows "+strings.Join(st.titles, ","), func() bool {
			_, frames, _ := dh.snapshot()
			return len(frames) > 0 && strings.Join(showsTitles(text(frames[len(frames)-1])), ",") == strings.Join(st.titles, ",")
		})
	}
}
