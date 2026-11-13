package install_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/install"
)

// record is install.toml as install-autodoc.sh writes it (write_record): aligned keys, quoted values.
const record = `method       = "script"
version      = "v0.3.12"
channel      = "stable"
bin          = "/home/u/.local/bin/autodoc"
launcher     = "/home/u/.local/share/applications/autodoc.desktop"
update       = "/home/u/.local/state/autodoc/update.sh"
installed_at = "2026-11-07T12:00:00Z"
updated_at   = "2026-11-07T12:30:00Z"
`

// TestTheScriptsRecordReads: every field the script writes is read, and the record says an Update
// can run.
func TestTheScriptsRecordReads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "install.toml")
	if err := os.WriteFile(p, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok, err := install.Read(p)
	if err != nil || !ok {
		t.Fatalf("Read: ok %v, %v", ok, err)
	}
	want := install.Record{
		Method: "script", Version: "v0.3.12", Channel: "stable",
		Bin:         "/home/u/.local/bin/autodoc",
		Launcher:    "/home/u/.local/share/applications/autodoc.desktop",
		Update:      "/home/u/.local/state/autodoc/update.sh",
		InstalledAt: "2026-11-07T12:00:00Z", UpdatedAt: "2026-11-07T12:30:00Z",
	}
	if r != want {
		t.Fatalf("Read:\n got %+v\nwant %+v", r, want)
	}
	if !r.Scripted() {
		t.Fatal("a script install's record: Scripted is false")
	}
}

// TestNoRecordIsAnotherInstall: no file is no record and no error; another method, or no update.sh,
// offers no Update; a file that doesn't parse is an error.
func TestNoRecordIsAnotherInstall(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := install.Read(filepath.Join(dir, "install.toml")); ok || err != nil {
		t.Fatalf("no file: ok %v, %v", ok, err)
	}
	for _, r := range []install.Record{{Method: "brew", Update: "/u.sh"}, {Method: "script"}} {
		if r.Scripted() {
			t.Fatalf("%+v: Scripted", r)
		}
	}
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("method = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := install.Read(bad); ok || err == nil {
		t.Fatalf("a record that doesn't parse: ok %v, %v", ok, err)
	}
}

// The update script's own extension picks its program, whichever OS's path the record holds.
func TestAnUpdateScriptRunsInItsOwnProgram(t *testing.T) {
	const ps = "powershell.exe"
	flags := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	for _, c := range []struct {
		update, program string
		args            []string
	}{
		{"/home/u/.local/state/autodoc/update.sh", "/bin/sh", []string{"/home/u/.local/state/autodoc/update.sh"}},
		{`C:\Users\A B\AppData\Local\autodoc\update.ps1`, ps, append(slices.Clone(flags), `C:\Users\A B\AppData\Local\autodoc\update.ps1`)},
		{`C:\autodoc\UPDATE.PS1`, ps, append(slices.Clone(flags), `C:\autodoc\UPDATE.PS1`)},
		{`/odd.ps1/update.sh`, "/bin/sh", []string{`/odd.ps1/update.sh`}},
		{`C:\odd.ps1\update`, "/bin/sh", []string{`C:\odd.ps1\update`}},
	} {
		program, args := install.Record{Update: c.update}.UpdateCommand()
		if program != c.program || !slices.Equal(args, c.args) {
			t.Errorf("UpdateCommand of %q = %q %q; want %q %q", c.update, program, args, c.program, c.args)
		}
	}
}
