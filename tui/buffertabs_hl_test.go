package tui

import (
	"slices"
	"testing"
)

// H and L between the buffers (bufferChord): bufferline's Shift+h / Shift+l, in Vim's Normal mode.

// shownPath is the path of the buffer shown, read on the loop: "" for a draft.
func (r *running) shownPath() string {
	return onLoop(r, func() string {
		if r.h.active == nil {
			return ""
		}
		return r.h.active.file.path
	})
}

// TestHAndLSwitchBuffers: in Normal mode L shows the next buffer and H the one before, in the
// order the tabs show them, wrapping at both ends. Insert mode types them; Visual mode keeps Vim's
// H and L (the screen's top and bottom line).
func TestHAndLSwitchBuffers(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, f := range []string{"a.md", "b.md", "c.md"} {
		r.openByPicker(t, f)
		r.waitFile(t, f)
	}
	order := onLoop(r, func() []string {
		var out []string
		for _, b := range r.h.buffers {
			out = append(out, b.file.path)
		}
		return out
	})
	if !slices.Equal(order, []string{"a.md", "b.md", "c.md"}) {
		t.Fatalf("fixture: buffers %q, want a.md, b.md, c.md", order)
	}
	r.keys(t, esc())
	for _, step := range []struct {
		key  rune
		want string
	}{{'L', "a.md"}, {'L', "b.md"}, {'H', "a.md"}, {'H', "c.md"}} { // c → a wraps forward, a → c back
		r.keys(t, key(step.key))
		r.s.WaitFor(t, string(step.key)+" shows "+step.want, func(string) bool { return r.shownPath() == step.want })
	}

	// Insert mode: H is typed, the buffer stays
	r.keys(t, key('i'), key('H'), esc())
	r.waitTyped(t)
	if got := r.shownPath(); got != "c.md" || r.editorText() != "Hccc\n" {
		t.Fatalf("Insert mode's H: shown %q, text %q; want c.md, Hccc", got, r.editorText())
	}
	// Visual mode: Vim's H, the buffer stays
	r.keys(t, key('v'), key('H'), esc())
	if got := r.shownPath(); got != "c.md" {
		t.Fatalf("Visual mode's H showed %q, want c.md still", got)
	}
}

// TestHAndLStayOutOfTheTerminal: with the keyboard in the terminal, L is the shell's, and the
// buffer stays.
func TestHAndLStayOutOfTheTerminal(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.newEmptyBuffer(); return true })
	r.s.WaitFor(t, "two buffers, the draft shown", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.buffers) == 2 }) && r.shownPath() == ""
	})
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, key('L'), key('H'), key('L'))
	r.s.WaitForText(t, "LHL") // the shell echoed them: they went to the terminal
	if got := r.shownPath(); got != "" {
		t.Fatalf("L in the terminal showed %q, want the draft still", got)
	}
}
