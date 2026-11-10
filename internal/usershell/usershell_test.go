package usershell

import (
	"encoding/json"
	"errors"
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
	written, err := s.Args(args)
	if err != nil {
		t.Fatalf("Args(%q): %v", args, err)
	}
	prog, pargs := s.Run(line + written)
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
	`say "hi" now`, `it's`, `C:\o'brien dir\`, "", `!x (a)`}

// carried is setupShaped and every hostile argument s Carries; refused, the hostile ones it doesn't.
func carried(s Shell) (carried, refused []string) {
	carried = slices.Clone(setupShaped)
	for _, a := range hostile {
		if s.Carries(a) == nil {
			carried = append(carried, a)
		} else {
			refused = append(refused, a)
		}
	}
	return carried, refused
}

// Args carries every argument it accepts whole to the program, through each shell this OS has: a
// setup's (a quote followed by a space inside a value would be split by Windows PowerShell 5.1's
// own passing) and each hostile one Carries accepts; each one it refuses is an error, with nothing
// written.
func TestArgsCarryEachArgumentWhole(t *testing.T) {
	for _, s := range shellsHere(t) {
		t.Run(s.Program, func(t *testing.T) {
			args, refused := carried(s)
			if got := throughArgs(t, s, os.Args[0], args); !reflect.DeepEqual(got, args) {
				t.Errorf("the program was given %q\nwant %q", got, args)
			}
			for _, a := range refused {
				if w, err := s.Args([]string{"x", a}); !errors.Is(err, ErrNotCarried) || w != "" {
					t.Errorf("%q: written %q, %v; want ErrNotCarried and nothing", a, w, err)
				}
			}
		})
	}
}

// Carries refuses, under either PowerShell, what cmd.exe reads again in a batch file's %* (% & | <
// > ^ and a line break), and nothing else; under /bin/sh nothing an argument can hold.
func TestCarriesRefusesWhatABatchFileReadsAgain(t *testing.T) {
	refusedHere := []string{"%PATH%", "100%", "a&b", "a | b", "<x>", "a^b", "two\nlines", "cr\r"}
	carriedEverywhere := []string{"", `{"k":"v w"}`, `a"b`, "it's", `ends\`, "$HOME", "!x", "(a)", "x; echo y", "naïve ‘q’ “q”"}
	for _, k := range []kind{powershell, winps} {
		for _, a := range refusedHere {
			if err := (Shell{kind: k}).Carries(a); !errors.Is(err, ErrNotCarried) {
				t.Errorf("kind %d, %q: %v; want ErrNotCarried", k, a, err)
			}
		}
		for _, a := range carriedEverywhere {
			if err := (Shell{kind: k}).Carries(a); err != nil {
				t.Errorf("kind %d, %q: %v; want it carried", k, a, err)
			}
		}
	}
	for _, a := range append(refusedHere, carriedEverywhere...) {
		if err := (Shell{kind: posix}).Carries(a); err != nil {
			t.Errorf("posix, %q: %v; want it carried", a, err)
		}
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
		if got, err := c.s.Args(args); err != nil || got != c.want {
			t.Errorf("%s: %s, %v; want %s", c.s.Program, got, err, c.want)
		}
		if got, err := c.s.Args(nil); err != nil || got != "" {
			t.Errorf("%s: no args written %q, %v", c.s.Program, got, err)
		}
	}
}
