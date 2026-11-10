//go:build windows

package procenv

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeProgram writes a program named name in dir: one Windows runs, name.exe, when runnable, and a
// file of the bare name, which no PATHEXT extension matches, when not.
func writeProgram(t *testing.T, dir, name string, runnable bool) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if runnable {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A variable's name is matched as Windows matches it, case-blind: the environment's LOCALAPPDATA is
// what a caller asks for as LocalAppData, and With replaces it whatever case either is spelled in.
func TestWindowsNamesMatchWhateverTheirCase(t *testing.T) {
	e := Env{"LOCALAPPDATA=C:\\data"}
	if got := e.Get("LocalAppData"); got != `C:\data` {
		t.Errorf("Get(LocalAppData) = %q, want the LOCALAPPDATA set", got)
	}
	w := e.With("LocalAppData=D:\\other")
	if len(w) != 1 || w.Get("LOCALAPPDATA") != `D:\other` {
		t.Errorf("With(LocalAppData=…) = %q, want it to replace LOCALAPPDATA", w)
	}
}

// errClass names the error a lookup gave, by the sentinel it wraps.
func errClass(err error) string {
	for _, s := range []struct {
		name string
		err  error
	}{{"ErrDot", exec.ErrDot}, {"ErrNotFound", exec.ErrNotFound}, {"ErrNotExist", fs.ErrNotExist}, {"ErrPermission", fs.ErrPermission}} {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// The contract on Windows: an Env's LookPath is exec.LookPath with the same variables and the same
// current folder, case by case; each case's own answer is stated too, so exec agreeing with a
// wrong expectation fails the case rather than passing it.
func TestTheWindowsLookupIsExecLookPaths(t *testing.T) {
	bin, here := t.TempDir(), t.TempDir()
	for _, f := range []string{"tool.exe", "weird.bat.exe", "note.txt", "both.exe"} {
		if err := os.WriteFile(filepath.Join(bin, f), []byte("MZ"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"here.exe", "both.exe"} {
		if err := os.WriteFile(filepath.Join(here, f), []byte("MZ"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(bin, "folder.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	relBin, err := filepath.Rel(here, bin)
	if err != nil {
		t.Fatal(err)
	}
	const noDot = "NoDefaultCurrentDirectoryInExePath"
	cases := []struct {
		desc, name, cwd, path, pathext string
		noDot                          bool
		want, wantErr                  string
	}{
		{"a path without its extension", filepath.Join(bin, "tool"), here, "", "", false, filepath.Join(bin, "tool.exe"), "nil"},
		{"a path with its extension", filepath.Join(bin, "tool.exe"), here, "", "", false, filepath.Join(bin, "tool.exe"), "nil"},
		{"a path to nothing", filepath.Join(bin, "missing"), here, "", "", false, "", "ErrNotFound"},
		{"a path with an extension to nothing", filepath.Join(bin, "missing.txt"), here, "", "", false, "", "ErrNotExist"},
		{"a bare name", "tool", here, bin, "", false, filepath.Join(bin, "tool.exe"), "nil"},
		{"a name with an extension PATHEXT lacks", "note.txt", here, bin, "", false, filepath.Join(bin, "note.txt"), "nil"},
		{"a name whose extension is not its file's", "weird.bat", here, bin, "", false, filepath.Join(bin, "weird.bat.exe"), "nil"},
		{"a folder is no program", "folder", here, bin, "", false, "", "ErrNotFound"},
		{"the current folder's program", "here", here, bin, "", false, filepath.Join(".", "here.exe"), "ErrDot"},
		{"the current folder skipped", "here", here, bin, "", true, "", "ErrNotFound"},
		{"the current folder's, another than PATH's", "both", here, bin, "", false, filepath.Join(".", "both.exe"), "ErrDot"},
		{"the current folder's, the same as PATH's", "both", bin, bin, "", false, filepath.Join(bin, "both.exe"), "nil"},
		{"an empty PATH entry is not the current folder", "here", here, ";" + bin, "", true, "", "ErrNotFound"},
		{"a PATHEXT of its own", "note", here, bin, ".TXT", false, filepath.Join(bin, "note.txt"), "nil"},
		{"a PATHEXT of no extensions", "tool.exe", here, bin, ";", false, filepath.Join(bin, "tool.exe"), "nil"},
		{"a PATHEXT of no extensions adds none", "tool", here, bin, ";", false, "", "ErrNotFound"},
		{"a relative PATH entry", "tool", here, relBin, "", false, filepath.Join(relBin, "tool.exe"), "ErrDot"},
		{"no name", "", here, bin, "", false, "", "ErrNotFound"},
		{"the parent folder", "..", here, bin, "", false, "", "ErrNotFound"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			t.Chdir(c.cwd)
			t.Setenv("PATH", c.path)
			t.Setenv("PATHEXT", c.pathext)
			t.Setenv(noDot, "")
			e := Env{"PATH=" + c.path, "PATHEXT=" + c.pathext}
			if c.noDot {
				e = append(e, noDot+"=")
			} else if err := os.Unsetenv(noDot); err != nil {
				t.Fatal(err)
			}
			wantPath, wantErr := exec.LookPath(c.name)
			if wantPath != c.want || errClass(wantErr) != c.wantErr {
				t.Fatalf("exec.LookPath(%q) = %q, %v; the case says %q, %s", c.name, wantPath, wantErr, c.want, c.wantErr)
			}
			got, err := LookPath(e, c.name)
			if got != wantPath || errClass(err) != errClass(wantErr) {
				t.Errorf("LookPath(%q) = %q, %v; exec.LookPath finds %q, %v", c.name, got, err, wantPath, wantErr)
			}
		})
	}
}

// On Windows a program's name is its file's base name lower-cased, less an extension PATHEXT names:
// claude.cmd and CLAUDE.EXE are claude; a.sh, an extension PATHEXT doesn't name, is a.sh.
func TestAProgramsNameIsLessItsPATHEXTExtensionOnWindows(t *testing.T) {
	e := Env{"PATHEXT=.COM;.EXE;.BAT;.CMD"}
	for file, want := range map[string]string{`C:\npm\claude.cmd`: "claude", `CLAUDE.EXE`: "claude", `D:/tools/mise.exe`: "mise",
		`a.sh`: "a.sh", `claude`: "claude", `env.bat`: "env"} {
		if got := ProgramName(e, file); got != want {
			t.Errorf("ProgramName(%q) = %q, want %q", file, got, want)
		}
	}
}
