package usershell

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// The test binary is its own helper: run with argvHelper set, it prints the arguments it was given
// as JSON and exits, so a cell sees exactly what a shell passed a program.
const argvHelper = "AUTODOC_USERSHELL_ARGV"

func TestMain(m *testing.M) {
	if os.Getenv(argvHelper) == "1" {
		b, _ := json.Marshal(os.Args[1:])
		fmt.Print(string(b))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// hostile are arguments whose characters each shell would take for its own: spaces, quotes of each
// kind, variables, substitutions, separators, pipes, redirections, backslashes and a percent sign.
var hostile = []string{
	"a b", "it's", `say "hi"`, "$HOME", "${HOME}", "%PATH%", "100%", "x; echo y", "$(echo z)", "`echo z`",
	"a & b", "a | b", "a > b", "a ^ b", `back\slash\`, `C:\Program Files\x\`, "naïve ‘q’ “q”", "(a)", "!x",
}

// throughShell runs helper with args through s, each Quote'd, and answers what helper was given.
func throughShell(t *testing.T, s Shell, args []string) []string {
	t.Helper()
	line := s.Quote(os.Args[0])
	if s.kind == powershell {
		line = "& " + line // PowerShell runs a quoted program only through its call operator
	}
	for _, a := range args {
		line += " " + s.Quote(a)
	}
	prog, pargs := s.Run(line)
	cmd := exec.Command(prog, pargs...)
	cmd.Env = procenv.Env(os.Environ()).With(argvHelper + "=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %q: %v (%s)", prog, pargs, err, out)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s printed %q", prog, out)
	}
	return got
}

// Every argument Quote'd reaches the program whole and as itself, through each shell this OS has:
// none of the other shells' characters, or its own, breaks it or is read.
func TestAQuotedArgumentReachesTheProgramAsItself(t *testing.T) {
	for _, s := range shellsHere(t) {
		t.Run(s.Program, func(t *testing.T) {
			for _, a := range hostile {
				if got := throughShell(t, s, []string{a}); !reflect.DeepEqual(got, []string{a}) {
					t.Errorf("%q reached the program as %q", a, got)
				}
			}
			if got := throughShell(t, s, hostile); !reflect.DeepEqual(got, hostile) {
				t.Errorf("all at once reached the program as %q", got)
			}
		})
	}
}

// Run is each kind's way to run one line.
func TestEachShellRunsALineItsWay(t *testing.T) {
	for _, c := range []struct {
		s    Shell
		want []string
	}{
		{Shell{"/bin/sh", posix}, []string{"-c", "echo hi"}},
		{Shell{"pwsh", powershell}, []string{"-NoLogo", "-NoProfile", "-Command", "echo hi"}},
		{Shell{`C:\Windows\System32\cmd.exe`, cmd}, []string{"/D", "/S", "/C", "echo hi"}},
	} {
		prog, args := c.s.Run("echo hi")
		if prog != c.s.Program || !reflect.DeepEqual(args, c.want) {
			t.Errorf("%s: %s %q, want %q", c.s.Program, prog, args, c.want)
		}
	}
}
