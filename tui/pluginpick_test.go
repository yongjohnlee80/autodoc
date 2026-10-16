package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE SPC p CHOOSER (ADR 1792132889 §5) — SPC p lists the plugins with commands; a plugin's letter,
// the user's, or the cursor then Enter shows its card; * gives the plugin under the cursor a letter.

func backspace() tuicore.Event {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyBackspace}
}

// pluginCardOf opens SPC p and chooses the plugin by the cursor: j or k to it, then Enter. It waits
// for the plugin's card.
func (r *running) pluginCardOf(t *testing.T, name string) {
	t.Helper()
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugins")
	i, at, title := onLoop3(r, func() (int, int, string) {
		pl := r.h.plugins
		i := slices.IndexFunc(pl.chooser, func(p pluginRow) bool { return p.key == name })
		return i, pl.chooserAt, pl.pluginTitle(name)
	})
	if i < 0 {
		t.Fatalf("%s is not on the chooser:\n%s", name, r.s)
	}
	for ; at < i; at++ {
		r.keys(t, key('j'))
	}
	for ; at > i; at-- {
		r.keys(t, key('k'))
	}
	r.keys(t, enter())
	r.s.WaitForText(t, "SPC p › "+title)
}

func onLoop3[A, B, C any](r *running, fn func() (A, B, C)) (A, B, C) {
	type abc struct {
		a A
		b B
		c C
	}
	v := onLoop(r, func() abc { a, b, c := fn(); return abc{a, b, c} })
	return v.a, v.b, v.c
}

// twoPlugins installs alpha and beta, each with a command on s, alpha's another on h.
func twoPlugins(t *testing.T) string {
	dir := t.TempDir()
	installPlugin2(t, dir, "alpha", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"a\"\ntitle = \"Do a\"\nkey = \"s\"\n"+
		"[[commands]]\nid = \"hello\"\ntitle = \"Say hello\"\nkey = \"h\"\n")
	installPlugin2(t, dir, "beta", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n"+
		"[[commands]]\nid = \"x\"\ntitle = \"Do x\"\nkey = \"s\"\n")
	return dir
}

// TestEachPluginsCardRunsItsOwnLetter (§5.1, §5.2): both plugins bind s, and each card's s runs its
// own plugin's command. Backspace on a card goes back to the chooser; q closes the chooser.
func TestEachPluginsCardRunsItsOwnLetter(t *testing.T) {
	r, logs := pluginTUI(t, twoPlugins(t))
	r.pluginCardOf(t, "beta")
	r.s.WaitForText(t, "s  Do x")
	r.keys(t, key('s'))
	waitSaid(t, logs, "beta", "command x")

	r.pluginCardOf(t, "alpha")
	r.s.WaitForText(t, "s  Do a")
	r.keys(t, backspace())
	r.s.WaitForText(t, "SPC p — plugins")
	r.keys(t, enter()) // the cursor kept on alpha
	r.s.WaitForText(t, "SPC p › Alpha")
	r.keys(t, key('s'))
	waitSaid(t, logs, "alpha", "command a")
	if got := said(t, logs, "beta"); slices.Contains(got, "command a") {
		t.Fatalf("alpha's s reached beta: %q", got)
	}

	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugins")
	r.keys(t, key('q'))
	r.s.WaitFor(t, "q closed the chooser", func(sc string) bool { return !strings.Contains(sc, "SPC p — plugins") })
}

// TestStarGivesAPluginALetter (§5.3): * then a letter gives the plugin under the cursor that letter,
// stored, and the letter then shows its card: g among them, which a focused list would have taken. A
// letter another plugin has moves, the chooser saying so. One of the chooser's own (j, k, q) is
// refused, and nothing moves. Backspace after * removes the letter. F1 names the keys.
func TestStarGivesAPluginALetter(t *testing.T) {
	dir := twoPlugins(t)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: maps.Clone(testPrefs)})
	r, logs := pluginTUIOn(t, d, dir, "")
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugins")
	r.keys(t, key('j'), key('*')) // beta
	r.s.WaitForText(t, "a letter or digit for Beta")
	r.keys(t, key('g'))
	r.s.WaitForText(t, "g is Beta's")
	storedPref(t, d, r, "tui.plugin.beta.letter", "g")
	r.s.WaitForText(t, "g  Beta")
	r.keys(t, key('g'))
	r.s.WaitForText(t, "SPC p › Beta")
	r.keys(t, key('s'))
	waitSaid(t, logs, "beta", "command x")
	r.s.WaitFor(t, "F1 names beta's keys", func(string) bool {
		help := strings.Join(strings.Fields(onLoop(r, func() string { return r.h.helpNow })), " ")
		return strings.Contains(help, "plugin.beta.x — · SPC p g s · Beta: Do x")
	})

	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugins")
	r.keys(t, key('k'), key('*'), key('j')) // alpha; j is the chooser's own
	r.s.WaitForText(t, "j moves down: * again for another letter")
	if at, l := onLoop2(r, func() (int, string) { return r.h.plugins.chooserAt, r.h.prefs.pluginLetter["alpha"] }); at != 0 || l != "" {
		t.Fatalf("after * j: the cursor on %d, alpha's letter %q; want 0 and none", at, l)
	}
	r.keys(t, key('*'), key('g')) // beta's g moves to alpha
	r.s.WaitForText(t, "g is Alpha's (it was Beta's)")
	storedPref(t, d, r, "tui.plugin.alpha.letter", "g")
	storedPref(t, d, r, "tui.plugin.beta.letter", "")
	r.keys(t, key('g'))
	r.s.WaitForText(t, "SPC p › Alpha")

	r.keys(t, backspace())
	r.s.WaitForText(t, "SPC p — plugins")
	// after a restart, alpha's letter is g: the stored one
	r2, _ := pluginTUIOn(t, d, dir, "")
	r2.leader(t, 'p')
	r2.s.WaitForText(t, "SPC p — plugins")
	r2.s.WaitForText(t, "g  Alpha")
	r2.keys(t, key('g'))
	r2.s.WaitForText(t, "SPC p › Alpha")

	r.keys(t, key('*'), backspace())
	r.s.WaitForText(t, "Alpha has no letter")
	storedPref(t, d, r, "tui.plugin.alpha.letter", "")
	r.keys(t, key('g')) // no plugin's now: the chooser stays, no card opened
	r.s.WaitForText(t, "SPC p — plugins")
	if sc := r.s.String(); strings.Contains(sc, "SPC p › ") {
		t.Fatalf("g opened a card with no plugin's letter g:\n%s", sc)
	}
}

func onLoop2[A, B any](r *running, fn func() (A, B)) (A, B) {
	a, b, _ := onLoop3(r, func() (A, B, struct{}) { a, b := fn(); return a, b, struct{}{} })
	return a, b
}

// TestPluginLettersFromThePreferences (§5.3, the restart): the letters are read from the stored
// preferences. Two plugins the preferences give one letter (edited by hand): the first by name keeps
// it, and the other says why; a reserved one is refused. Binding the letter to the second takes it
// from every other, so the preferences then name one plugin per letter.
func TestPluginLettersFromThePreferences(t *testing.T) {
	dir := twoPlugins(t)
	installPlugin2(t, dir, "gamma", "dialog", "echo", "[[commands]]\nid = \"g\"\ntitle = \"Do g\"\nkey = \"h\"\n")
	prefs := maps.Clone(testPrefs)
	maps.Copy(prefs, map[string]string{"tui.plugin.alpha.letter": "d", "tui.plugin.beta.letter": "d", "tui.plugin.gamma.letter": "k"})
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	r, _ := pluginTUIOn(t, d, dir, "")
	r.leader(t, 'p')
	r.s.WaitForText(t, "SPC p — plugins")
	for _, want := range []string{"d  Alpha", "Beta   1 command — d is Alpha's", "Gamma  1 command — k moves up"} {
		r.s.WaitForText(t, want)
	}
	r.keys(t, key('j'), key('*'), key('d')) // beta takes d
	r.s.WaitForText(t, "d is Beta's (it was Alpha's)")
	storedPref(t, d, r, "tui.plugin.alpha.letter", "")
	r.keys(t, key('d'))
	r.s.WaitForText(t, "SPC p › Beta")
}

// TestNoPluginHasCommands: the chooser says how to add one.
func TestNoPluginHasCommands(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "plain", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n")
	r, _ := pluginTUI(t, dir)
	r.leader(t, 'p')
	r.s.WaitForText(t, "no plugin has commands yet: Plugins › Add from a git URL… adds one")
	r.keys(t, enter(), key('*'), key('a')) // nothing to choose or bind
	if l := onLoop(r, func() map[string]string { return r.h.prefs.pluginLetter }); len(l) != 0 {
		t.Fatalf("a letter bound with no plugin: %v", l)
	}
	r.s.WaitForText(t, "SPC p — plugins")
}
