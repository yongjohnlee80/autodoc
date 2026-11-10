package todo

import "testing"

// Under Windows' rule, run on every OS: a ref given in the OS's own form, a variable's value in it,
// and what ~ or a variable expands to are read in the slash form, so a ref under a known root is
// written as that root's variable, and one under none is kept in the slash form. Roots are compared
// exactly: a ref in another case than the root's is under none.
func TestARefInWindowsFormIsReadInTheSlashForm(t *testing.T) {
	s := scope{windows: true, kbRoot: "C:/kb", home: "C:/Users/u", user: map[string]string{"DOCS": `D:\docs\`}}
	for in, want := range map[string]string{
		`C:\kb\adrs\x.md`: "C:/kb/adrs/x.md", `\\srv\sh\r.md`: "//srv/sh/r.md", "$DOCS/x.md": "D:/docs/x.md",
		`$DOCS\y.md`: "D:/docs/y.md", "~/n.md": "C:/Users/u/n.md", `\\?\C:\kb\v.md`: "C:/kb/v.md", "adrs/x.md": "adrs/x.md",
	} {
		if got := s.resolve(in); got.unresolved || got.path != want {
			t.Errorf("resolve(%q) = %+v, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		`C:\kb\adrs\x.md`: "$KB_ROOT/adrs/x.md", `D:\docs\y.md`: "$DOCS/y.md", `\\srv\sh\r.md`: "//srv/sh/r.md",
		"C:/kb/a.md": "$KB_ROOT/a.md", "$KB_ROOT/a.md": "$KB_ROOT/a.md", "~/n.md": "$HOME/n.md",
		// compared exactly: a ref in another case is another spelling, under no root, kept absolute
		"c:/kb/a.md": "c:/kb/a.md", `C:\KB\a.md`: "C:/KB/a.md", "C:/kbx/a.md": "C:/kbx/a.md",
	} {
		if got := s.symbolize(in); got != want {
			t.Errorf("symbolize(%q) = %q, want %q", in, got, want)
		}
	}
}

// Under Unix' rule a backslash is a name's own character: a ref holding one is read and written
// as it is, under a root or not, and a Windows-looking path is no absolute path.
func TestARefWithABackslashIsAsWrittenUnderUnixRules(t *testing.T) {
	s := scope{windows: false, kbRoot: "/kb", user: map[string]string{}}
	for in, want := range map[string]string{`/kb/a\b.md`: `/kb/a\b.md`, `/x/a\b.md`: `/x/a\b.md`, `C:\kb\a.md`: `C:\kb\a.md`} {
		if got := s.resolve(in); got.path != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got.path, want)
		}
	}
	for in, want := range map[string]string{`/kb/a\b.md`: `$KB_ROOT/a\b.md`, `/x/a\b.md`: `/x/a\b.md`, `C:\kb\a.md`: `C:\kb\a.md`,
		"/KB/a.md": "/KB/a.md"} { // a name's case is its own under Unix' rule
		if got := s.symbolize(in); got != want {
			t.Errorf("symbolize(%q) = %q, want %q", in, got, want)
		}
	}
}
