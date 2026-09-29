package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
)

func load(t *testing.T, body string) (*config.Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

func TestLoadFillsDefaults(t *testing.T) {
	c, err := load(t, "[server]\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Follow.PollInterval.Duration != config.DefaultPollInterval {
		t.Errorf("poll interval = %v", c.Follow.PollInterval.Duration)
	}
}

func TestLoadReadsEverySetting(t *testing.T) {
	c, err := load(t, `
[server]
socket = "/tmp/a.sock"
state_dir = "/tmp/autodoc-state"
data_dir = "/tmp/autodoc-data"
[follow]
poll_interval = "500ms"
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Socket != "/tmp/a.sock" || c.Server.StateDir != "/tmp/autodoc-state" || c.Server.DataDir != "/tmp/autodoc-data" ||
		c.Follow.PollInterval.Duration != 500*time.Millisecond {
		t.Errorf("server/follow = %+v %+v", c.Server, c.Follow)
	}
}

func TestLoadRejects(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"unknown key", "[server]\nsockets = \"/y\"\n", "unknown settings"},
		{"a [[workspace]] section", "[[workspace]]\nname = \"kb\"\nroot = \"/x\"\n", "kept in the store"},
		{"bad duration", "[follow]\npoll_interval = \"soon\"\n", "invalid"},
		{"not TOML", "[[server\n", "invalid"},
		{"an [embedding] section", "[embedding]\nprovider = \"ollama\"\nmodel = \"m\"\n", "kept in the store"},
	} {
		_, err := load(t, c.body)
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalid mentioning %q", c.name, err, c.want)
		}
	}
}

// NormalizeWorkspace is what workspace.add checks: the rules [[workspace]] had.
func TestNormalizeWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	w, err := config.NormalizeWorkspace(config.Workspace{Name: "kb", Root: "~/notes"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Root != filepath.Join(home, "notes") {
		t.Errorf("root = %q, want ~ expanded", w.Root)
	}
	if !reflect.DeepEqual(w.Include, config.DefaultInclude) || !reflect.DeepEqual(w.Exclude, config.DefaultExclude) {
		t.Errorf("patterns = %v / %v, want the defaults", w.Include, w.Exclude)
	}
	w, err = config.NormalizeWorkspace(config.Workspace{Name: "kb", Root: "/srv/kb", Include: []string{"docs/**/*.md", "*.markdown"}, Exclude: []string{}})
	if err != nil || len(w.Include) != 2 || len(w.Exclude) != 0 {
		t.Errorf("an explicit empty exclude must stay empty: %+v, %v", w, err)
	}
	for _, c := range []struct {
		name string
		w    config.Workspace
		want string
	}{
		{"relative root", config.Workspace{Name: "a", Root: "notes"}, "absolute"},
		{"no name", config.Workspace{Root: "/x"}, "name is required"},
		{"a name with a separator", config.Workspace{Name: "a/b", Root: "/x"}, "path separator"},
		{"a name with surrounding space", config.Workspace{Name: " a", Root: "/x"}, "surrounding space"},
		{"bad pattern", config.Workspace{Name: "a", Root: "/x", Include: []string{"["}}, "pattern"},
		{"absolute pattern", config.Workspace{Name: "a", Root: "/x", Exclude: []string{"/etc/**"}}, "pattern"},
	} {
		if _, err := config.NormalizeWorkspace(c.w); !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalid mentioning %q", c.name, err, c.want)
		}
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if p, _ := config.DefaultPath(); p != "/cfg/autodoc/config.toml" {
		t.Errorf("DefaultPath = %q", p)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if d, err := (config.Server{}).StateDirPath(); err != nil || d != filepath.Join(state, "autodoc") {
		t.Errorf("StateDirPath = %q, %v", d, err)
	} else if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
		t.Errorf("the state directory is %v, want 0700", fi.Mode().Perm())
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	if p, err := (config.Server{}).StorePath(); err != nil || p != filepath.Join(data, "autodoc", "autodoc.db") {
		t.Errorf("StorePath = %q, %v", p, err)
	} else if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Errorf("the store's directory is %v, want 0700", fi.Mode().Perm())
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if s, _ := (config.Server{}).SocketPath(); s != "/run/user/1000/autodoc.sock" {
		t.Errorf("SocketPath = %q", s)
	}
	if _, err := (config.Server{Socket: "/" + strings.Repeat("x", 120)}).SocketPath(); !errors.Is(err, config.ErrInvalid) {
		t.Errorf("an over-long socket path: %v, want ErrInvalid before bind", err)
	}
}

// TestMissingFileIsDefaults: no file is every default, as AutoDB's; an unreadable
// one is an error, but not an invalid configuration.
func TestMissingFileIsDefaults(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Follow.PollInterval.Duration != config.DefaultPollInterval {
		t.Errorf("defaults %+v", c)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	p := filepath.Join(t.TempDir(), "locked.toml")
	if err := os.WriteFile(p, []byte("[follow]\n"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil || errors.Is(err, config.ErrInvalid) {
		t.Errorf("an unreadable file: %v", err)
	}
}
