package usershell

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
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
// kind, variables, substitutions, separators, pipes, redirections, backslashes, a percent sign and
// none at all.
var hostile = []string{
	"a b", "it's", `say "hi"`, "$HOME", "${HOME}", "%PATH%", "100%", "x; echo y", "$(echo z)", "`echo z`",
	"a & b", "a | b", "a > b", "a ^ b", `back\slash\`, `C:\Program Files\x\`, "naïve ‘q’ “q”", "(a)", "!x", "",
}

// throughShell runs helper with args through s, each Quote'd, and answers what helper was given.
func throughShell(t *testing.T, s Shell, args []string) []string {
	t.Helper()
	line := s.Quote(os.Args[0])
	if s.kind == powershell || s.kind == winps {
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
		{Shell{"pwsh", powershell}, []string{"-NoLogo", "-NoProfile", "-Command", "$PSNativeCommandArgumentPassing = 'Standard'; echo hi"}},
		{Shell{"powershell", winps}, []string{"-NoLogo", "-NoProfile", "-Command", "echo hi"}},
	} {
		prog, args := c.s.Run("echo hi")
		if prog != c.s.Program || !reflect.DeepEqual(args, c.want) {
			t.Errorf("%s: %s %q, want %q", c.s.Program, prog, args, c.want)
		}
	}
}

// throughArgs runs program (an .exe, or a batch file that runs one) through s with args written by
// Args, and answers what the program was given.
func throughArgs(t *testing.T, s Shell, program string, args []string) []string {
	t.Helper()
	line := s.Quote(program)
	if s.kind == powershell || s.kind == winps {
		line = "& " + line
	}
	prog, pargs := s.Run(line + s.Args(args))
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

// setupShaped are arguments as an agent's setup gives them: a JSON config with a space inside a
// value, a quote with a space after it, a path with an apostrophe and a last backslash, and none.
var setupShaped = []string{`{"mcpServers":{"autodoc":{"command":"C:\\Program Files\\autodoc.exe","args":["mcp serve"]}}}`,
	`say "hi" now`, `it's`, `C:\o'brien dir\`, "", `a & b`}

// Args carries every argument whole to the program, through each shell this OS has: the hostile
// ones, and a setup's, which a quote followed by a space inside a value would split under Windows
// PowerShell 5.1's own passing. Under 5.1 a %NAME% naming one of its variables is that variable's
// value (the --% text's one expansion), so %PATH% is not sent there.
func TestArgsCarryEachArgumentWhole(t *testing.T) {
	for _, s := range shellsHere(t) {
		t.Run(s.Program, func(t *testing.T) {
			args := append(slices.Clone(setupShaped), hostile...)
			if s.kind == winps {
				args = slices.DeleteFunc(args, func(a string) bool { return a == "%PATH%" })
			}
			if got := throughArgs(t, s, os.Args[0], args); !reflect.DeepEqual(got, args) {
				t.Errorf("the program was given %q\nwant %q", got, args)
			}
		})
	}
}

// Args writes each kind's way: POSIX and pwsh each word Quote'd; Windows PowerShell 5.1 after --%,
// each as a Windows program reads an argument.
func TestArgsAreWrittenEachShellsWay(t *testing.T) {
	args := []string{"a b", "", `a"b`, `ends\`, "it's"}
	for _, c := range []struct {
		s    Shell
		want string
	}{
		{Shell{"/bin/sh", posix}, ` 'a b' '' 'a"b' 'ends\' 'it'\''s'`},
		{Shell{"pwsh", powershell}, ` 'a b' '' 'a"b' 'ends\' 'it''s'`},
		{Shell{"powershell", winps}, ` --% "a b" "" "a\"b" "ends\\" "it's"`},
	} {
		if got := c.s.Args(args); got != c.want {
			t.Errorf("%s: %s, want %s", c.s.Program, got, c.want)
		}
		if got := c.s.Args(nil); got != "" {
			t.Errorf("%s: no args written %q", c.s.Program, got)
		}
	}
}
