package usershell

import "strings"

// Word is one word of a command line as its shell splits it before expanding anything: Text with
// its quotes and escapes taken out, and Bare, how many of its leading bytes stood unquoted (a
// NAME=value assignment needs its NAME= so).
type Word struct {
	Text string
	Bare int
}

// Assignment reports whether w is a NAME=value assignment: its NAME= unquoted, as a POSIX shell
// requires, whatever quoting its value has.
func (w Word) Assignment() bool {
	i := strings.IndexByte(w.Text, '=')
	if i <= 0 || i >= w.Bare {
		return false
	}
	for j, c := range w.Text[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Words is line's words as the shell this OS runs command lines in (Commands) reads them:
// /bin/sh's on Unix, PowerShell's on Windows, whichever PowerShell is installed (both read words
// alike). Nothing is expanded or evaluated.
func Words(line string) []Word { return commandWords(line) }

// Words is line's words as s reads them (Words).
func (s Shell) Words(line string) []Word {
	if s.kind == powershell || s.kind == winps {
		return powerShellWords(line)
	}
	return POSIXWords(line)
}

// POSIXWords is line's words as a POSIX shell splits them before expanding them, whatever the OS:
// for text AutoDoc reads itself by those rules (env -S's string, a profile's NAME=value field).
// Blanks between words, '…' literal, "…" with \ escaping $ ` " \ and a newline, and \ outside
// quotes escaping the next byte. Expansions are left as written, and an unterminated quote runs to
// the end.
func POSIXWords(line string) []Word {
	w := newWords()
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case ' ', '\t', '\n':
			w.end()
		case '\'':
			w.quoted()
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				j = len(line) - i - 1
			}
			w.b.WriteString(line[i+1 : i+1+j])
			i += j + 1
		case '"':
			w.quoted()
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) && strings.IndexByte("$`\"\\\n", line[i+1]) >= 0 {
					i++
					if line[i] == '\n' {
						continue
					}
				}
				w.b.WriteByte(line[i])
			}
		case '\\':
			w.quoted()
			if i+1 < len(line) {
				i++
				if line[i] != '\n' {
					w.b.WriteByte(line[i])
				}
			}
		default:
			w.in = true
			w.b.WriteByte(c)
		}
	}
	w.end()
	return w.out
}

// powerShellWords is line's words as PowerShell splits a command's before expanding them: blanks
// between words; '…' literal, ” in it a quote; "…" with ` escaping the next character and "" in it
// a quote; ` outside quotes escaping the next character. A backslash is a character like any other
// (a Windows path's separator). The call operator & before a quoted path is a word of its own.
// Expansions are left as written, and an unterminated quote runs to the end.
func powerShellWords(line string) []Word {
	w := newWords()
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case ' ', '\t', '\n', '\r':
			w.end()
		case '\'':
			w.quoted()
			for i++; i < len(line); i++ {
				if line[i] == '\'' {
					if i+1 < len(line) && line[i+1] == '\'' {
						i++
						w.b.WriteByte('\'')
						continue
					}
					break
				}
				w.b.WriteByte(line[i])
			}
		case '"':
			w.quoted()
			for i++; i < len(line); i++ {
				if line[i] == '`' && i+1 < len(line) {
					i++
					w.b.WriteByte(line[i])
					continue
				}
				if line[i] == '"' {
					if i+1 < len(line) && line[i+1] == '"' {
						i++
						w.b.WriteByte('"')
						continue
					}
					break
				}
				w.b.WriteByte(line[i])
			}
		case '`':
			w.quoted()
			if i+1 < len(line) {
				i++
				if line[i] != '\n' {
					w.b.WriteByte(line[i])
				}
			}
		default:
			w.in = true
			w.b.WriteByte(c)
		}
	}
	w.end()
	return w.out
}

// words collects a line's words as a splitter reads them.
type words struct {
	out  []Word
	b    strings.Builder
	in   bool
	bare int
}

func newWords() *words { return &words{bare: -1} }

// quoted marks a quote or an escape: the word's bare prefix ends here.
func (w *words) quoted() {
	w.in = true
	if w.bare < 0 {
		w.bare = w.b.Len()
	}
}

// end ends the word under way, if any.
func (w *words) end() {
	if !w.in {
		return
	}
	if w.bare < 0 {
		w.bare = w.b.Len()
	}
	w.out = append(w.out, Word{Text: w.b.String(), Bare: w.bare})
	w.b.Reset()
	w.in, w.bare = false, -1
}
