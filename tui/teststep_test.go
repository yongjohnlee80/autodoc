package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// testStep runs script, a test plugin's install build, as `sh -c script` would, on every OS: the
// install cells' fixtures (commitPlugin) build through the test binary rather than a shell, which
// Windows does not have. It knows only what those builds say: commands joined by `&&` (each runs
// when the one before succeeded) or `;` (each runs regardless), each one of
//
//	true | false | exit N | echo WORDS | ls -A | env | mv FROM TO
//
// with `> FILE` or `>&2` after it, and words quoted as agents.ShellQuote quotes them ('…', with \'
// outside the quotes). Anything else is refused, exit status 127, saying what: a cell that needs
// more says so at once rather than building something else. Its answer is the exit status.
func testStep(script string) int {
	status := 0
	for _, part := range splitStep(script) {
		if part.and && status != 0 {
			continue
		}
		if status = runStepCommand(part.words); part.exit {
			return status
		}
	}
	return status
}

// stepPart is one command of a step: its words, whether it runs only after a success (&&), and
// whether it ends the step (exit).
type stepPart struct {
	words     []string
	and, exit bool
}

// splitStep is script's commands, in order, its words unquoted.
func splitStep(script string) []stepPart {
	var (
		parts []stepPart
		words []string
		word  strings.Builder
		in    bool // a word is under way: '' makes one too
		quote bool
		and   bool
	)
	endWord := func() {
		if in {
			words = append(words, word.String())
			word.Reset()
			in = false
		}
	}
	endPart := func(nextAnd bool) {
		endWord()
		if len(words) > 0 {
			parts = append(parts, stepPart{words: words, and: and, exit: words[0] == "exit"})
		}
		words, and = nil, nextAnd
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case quote:
			if c == '\'' {
				quote = false
			} else {
				word.WriteByte(c)
			}
		case c == '\'':
			quote, in = true, true
		case c == '\\' && i+1 < len(script):
			i++
			word.WriteByte(script[i])
			in = true
		case c == ' ' || c == '\t':
			endWord()
		case c == ';':
			endPart(false)
		case c == '&' && strings.HasPrefix(script[i:], "&&"):
			i++
			endPart(true)
		default:
			word.WriteByte(c)
			in = true
		}
	}
	endPart(false)
	return parts
}

// runStepCommand runs one command's words, its output redirected as its last words say.
func runStepCommand(words []string) int {
	out := io.Writer(os.Stdout)
	switch n := len(words); {
	case n >= 2 && words[n-1] == ">&2":
		out, words = os.Stderr, words[:n-1]
	case n >= 3 && words[n-2] == ">":
		f, err := os.Create(words[n-1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "step:", err)
			return 1
		}
		defer f.Close()
		out, words = f, words[:n-2]
	}
	if len(words) == 0 {
		return 0 // a redirection alone makes its file, as sh does
	}
	switch {
	case len(words) == 1 && words[0] == "true":
		return 0
	case len(words) == 1 && words[0] == "false":
		return 1
	case len(words) == 2 && words[0] == "exit":
		n, err := strconv.Atoi(words[1])
		if err != nil {
			break
		}
		return n
	case words[0] == "echo":
		fmt.Fprintln(out, strings.Join(words[1:], " "))
		return 0
	case len(words) == 2 && words[0] == "ls" && words[1] == "-A":
		entries, err := os.ReadDir(".")
		if err != nil {
			fmt.Fprintln(os.Stderr, "step:", err)
			return 2
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Fprintln(out, name)
		}
		return 0
	case len(words) == 1 && words[0] == "env":
		for _, kv := range os.Environ() {
			fmt.Fprintln(out, kv)
		}
		return 0
	case len(words) == 3 && words[0] == "mv":
		if err := os.Rename(words[1], words[2]); err != nil {
			fmt.Fprintln(os.Stderr, "step:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "step: not a command the test step knows: %q\n", words)
	return 127
}

// TestTheTestStepRunsWhatShWould: the install fixtures' step reads words as agents.ShellQuote
// quotes them, runs && only after a success and ; regardless, and refuses a command it does not
// know (exit status 127) rather than doing something else.
func TestTheTestStepRunsWhatShWould(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "it's here.txt")
	read := func() string {
		b, err := os.ReadFile(out)
		if err != nil {
			return "(none)"
		}
		return string(b)
	}
	if got := testStep("echo 'a  b' c > " + agents.ShellQuote(out)); got != 0 || read() != "a  b c\n" {
		t.Fatalf("echo with quoted words: status %d, %q", got, read())
	}
	if got := testStep("false && echo no > " + agents.ShellQuote(out)); got != 1 || read() != "a  b c\n" {
		t.Fatalf("&& after a failure: status %d, %q", got, read())
	}
	if got := testStep("false; echo yes > " + agents.ShellQuote(out)); got != 0 || read() != "yes\n" {
		t.Fatalf("; after a failure: status %d, %q", got, read())
	}
	if got := testStep("true && exit 4; echo after > " + agents.ShellQuote(out)); got != 4 || read() != "yes\n" {
		t.Fatalf("exit ends the step: status %d, %q", got, read())
	}
	if got := testStep("curl example.com"); got != 127 {
		t.Fatalf("a command the step does not know: status %d, want 127", got)
	}
}
