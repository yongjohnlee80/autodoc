package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// Two programs at once, each with an environment of its own (seams.env): each one's folders are
// its own, it finds its own program on its own PATH, and its shell has its own HOME and runs that
// program; the process's environment is the same after as before.
func TestATestsEnvironmentReachesOnlyItsOwnProgram(t *testing.T) {
	before := os.Environ()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{})
	const prog = "autodoc-env-cell"
	type program struct {
		name, home, bin string
		mark            int
		r               *running
	}
	var progs []*program
	for i, name := range []string{"one", "two"} {
		p := &program{name: name, home: t.TempDir(), bin: t.TempDir(), mark: 41 + i}
		if err := os.WriteFile(filepath.Join(p.bin, prog), []byte(fmt.Sprintf("#!/bin/sh\necho ran-%s-$((40+%d))\n", name, i+1)), 0o755); err != nil {
			t.Fatal(err)
		}
		s := defaultSeams
		s.env = procenv.Env(nil).With("SHELL=/bin/sh", "PS1=$ ", "HOME="+p.home, "PATH="+p.bin+":/usr/bin:/bin")
		p.r = runTUI(t, NewSession(d.sock, nil), Options{seams: &s})
		p.r.connected(t)
		progs = append(progs, p)
	}
	for _, p := range progs { // both are running
		r := p.r
		env := onLoop(r, func() procenv.Env { return r.h.seams.env })
		if home := termDir(env, "/no/such/workspace"); home != p.home {
			t.Errorf("%s: its shell's folder for a root it hasn't is %q, want its own home %q", p.name, home, p.home)
		}
		if found, err := procenv.LookPath(env, prog); err != nil || found != filepath.Join(p.bin, prog) {
			t.Errorf("%s: its program on its PATH: %q, %v; want its own, in %s", p.name, found, err, p.bin)
		}
		r.leader(t, '`')
		r.s.WaitForText(t, "┌ terminal")
		r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
		// the output alone holds what the marks expand to: the line typed holds the arithmetic
		r.keys(t, decltest.Type(fmt.Sprintf("[ \"$HOME\" = '%s' ] && echo home-$((%d+0)); %s\r", p.home, p.mark, prog))...)
		r.s.WaitForText(t, fmt.Sprintf("home-%d", p.mark))
		r.s.WaitForText(t, fmt.Sprintf("ran-%s-%d", p.name, p.mark))
	}
	if after := os.Environ(); !slices.Equal(before, after) {
		t.Errorf("the process's environment changed:\nbefore %q\nafter  %q", before, after)
	}
	if v := os.Getenv("HOME"); v == progs[0].home || v == progs[1].home {
		t.Errorf("the process's HOME is a program's own: %q", v)
	}
}
