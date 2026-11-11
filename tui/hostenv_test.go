package tui

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// envCellProgram is the program each of the environment cell's programs finds on its own PATH: this
// test binary under that name, which prints ran-NAME-MARK, NAME its program's (envCellEnv), MARK
// its argument.
const (
	envCellProgram = "autodoc-env-cell"
	envCellEnv     = "AUTODOC_TEST_ENV_CELL"
)

func init() {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == envCellProgram && os.Getenv(envCellEnv) != "" {
		mark := ""
		if len(os.Args) > 1 {
			mark = os.Args[1]
		}
		fmt.Printf("ran-%s-%s\n", os.Getenv(envCellEnv), mark)
		os.Exit(0)
	}
}

// Two programs at once, each with an environment of its own (seams.env): each one's folders are
// its own, it finds its own program on its own PATH, and its shell has its own home and runs that
// program; the process's environment is the same after as before. Its home is set by both names a
// home goes by (HOME, and the OS's own: userdirs.HomeVariable), and its shell is the line shell
// (dockshell_test.go), so the cell runs on every OS.
func TestATestsEnvironmentReachesOnlyItsOwnProgram(t *testing.T) {
	before := os.Environ()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{})
	type program struct {
		name, home, bin string
		mark            int
		r               *running
	}
	var progs []*program
	for i, name := range []string{"one", "two"} {
		// the shell's folders, not t.TempDir's: on Windows a running program's file, or a process's
		// working folder, can't be removed while the shell may still be exiting (shellTempDir)
		// its home has a space in its name: a path with whitespace reaches its shell whole
		p := &program{name: name, home: shellTempDir(t, "autodoc-env home-"), bin: shellTempDir(t, "autodoc-env-bin-"), mark: 41 + i}
		standIn(t, p.bin, envCellProgram)
		sh := lineShellIn(t, p.bin)
		s := seamsWithEnv(t, nil, "SHELL="+sh, "PS1=$ ", lineShellEnv+"=1", envCellEnv+"="+name,
			"HOME="+p.home, userdirs.HomeVariable+"="+p.home, "PATH="+p.bin+string(os.PathListSeparator)+systemPath())
		p.r = runTUI(t, NewSession(d.sock, nil), Options{seams: s})
		p.r.connected(t)
		progs = append(progs, p)
	}
	for _, p := range progs { // both are running
		r := p.r
		env := onLoop(r, func() procenv.Env { return r.h.seams.env })
		if home := termDir(env, filepath.Join(t.TempDir(), "no-such-workspace")); home != p.home {
			t.Errorf("%s: its shell's folder for a root it hasn't is %q, want its own home %q", p.name, home, p.home)
		}
		if found, err := procenv.LookPath(env, envCellProgram); err != nil || found != filepath.Join(p.bin, exeName(envCellProgram)) {
			t.Errorf("%s: its program on its PATH: %q, %v; want its own, in %s", p.name, found, err, p.bin)
		}
		shellInTheTerminal(t, r)
		// the output alone holds what the shell prints: the line typed holds neither home-N nor ran-NAME-N
		r.keys(t, decltest.Type(fmt.Sprintf("ishome %d %s\r", p.mark, hex.EncodeToString([]byte(p.home))))...)
		r.s.WaitForText(t, fmt.Sprintf("home-%d", p.mark))
		r.keys(t, decltest.Type(fmt.Sprintf("run %s %d\r", envCellProgram, p.mark))...)
		r.s.WaitForText(t, fmt.Sprintf("ran-%s-%d", p.name, p.mark))
	}
	if after := os.Environ(); !slices.Equal(before, after) {
		t.Errorf("the process's environment changed:\nbefore %q\nafter  %q", before, after)
	}
	for _, name := range []string{"HOME", userdirs.HomeVariable} {
		if v := os.Getenv(name); v == progs[0].home || v == progs[1].home {
			t.Errorf("the process's %s is a program's own: %q", name, v)
		}
	}
}
