package usershell

import (
	"slices"
	"testing"
)

func texts(ws []Word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Text
	}
	return out
}

// A POSIX shell's words: a backslash escapes, '…' is literal, "…" keeps \ before anything it
// doesn't escape; an assignment is its NAME= unquoted.
func TestPOSIXWordsAreReadAsShDoes(t *testing.T) {
	for line, want := range map[string][]string{
		`/opt/a\ b/claude --model "haiku 4"`: {"/opt/a b/claude", "--model", "haiku 4"},
		`'it''s' "a\"b" "c\d"`:               {"its", `a"b`, `c\d`},
		`FOO=bar  claude`:                    {"FOO=bar", "claude"},
	} {
		if got := texts(POSIXWords(line)); !slices.Equal(got, want) {
			t.Errorf("POSIXWords(%q) = %q, want %q", line, got, want)
		}
	}
	if w := POSIXWords(`FOO=bar 'BAR'=x`); !w[0].Assignment() || w[1].Assignment() {
		t.Errorf("assignments: %+v, want the first alone", w)
	}
}

// PowerShell's words: a backslash is a character (a Windows path's separator); '…' with ” a quote;
// "…" and outside quotes, ` escapes; the call operator & is a word of its own.
func TestPowerShellWordsKeepABackslash(t *testing.T) {
	ps := Shell{kind: powershell}
	for line, want := range map[string][]string{
		`& 'C:\Program Files\x\claude.cmd' --model "haiku 4"`: {"&", `C:\Program Files\x\claude.cmd`, "--model", "haiku 4"},
		`C:\tools\claude.exe --x`:                             {`C:\tools\claude.exe`, "--x"},
		`'it''s' "a""b" "c` + "`" + `"d" e` + "`" + ` f`:      {"it's", `a"b`, `c"d`, "e f"},
	} {
		if got := texts(ps.Words(line)); !slices.Equal(got, want) {
			t.Errorf("PowerShell's Words(%q) = %q, want %q", line, got, want)
		}
		if got := texts((Shell{kind: winps}).Words(line)); !slices.Equal(got, want) {
			t.Errorf("Windows PowerShell's Words(%q) = %q, want %q", line, got, want)
		}
	}
	if got := texts((Shell{kind: posix}).Words(`a\ b`)); !slices.Equal(got, []string{"a b"}) {
		t.Errorf("/bin/sh's Words: %q, want POSIX's", got)
	}
}
