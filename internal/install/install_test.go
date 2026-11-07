package install_test

import (
	"os"
	"path/filepath"
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

// TestThePathFollowsXDGStateHome: $XDG_STATE_HOME when it's absolute, else ~/.local/state, as the
// scripts resolve it.
func TestThePathFollowsXDGStateHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if p, err := install.Path(); err != nil || p != "/xdg/state/autodoc/install.toml" {
		t.Fatalf("XDG_STATE_HOME set: %q, %v", p, err)
	}
	for _, v := range []string{"", "relative/state"} {
		t.Setenv("XDG_STATE_HOME", v)
		if p, err := install.Path(); err != nil || p != "/home/u/.local/state/autodoc/install.toml" {
			t.Fatalf("XDG_STATE_HOME %q: %q, %v", v, p, err)
		}
	}
}
